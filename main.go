// Originally written by @holiman, minor adjustments by me.

package main

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/sha3"
)

var (
	threadsFlag = &cli.IntFlag{
		Name:  "threads",
		Usage: "number of threads to search on",
		Value: int64(runtime.NumCPU()),
	}
	scoreFlag = &cli.IntFlag{
		Name:  "score",
		Usage: "minimum score number to report",
		Value: 5,
	}
	prefixFlag = &cli.StringFlag{
		Name:      "prefix",
		Usage:     "desired prefix in vanity address",
		Validator: checkHex,
		Value:     "0x0000",
	}
	suffixFlag = &cli.StringFlag{
		Name:      "suffix",
		Usage:     "desired suffix in vanity address",
		Validator: checkHex,
		Value:     "0xaaaa",
	}
	initcodeFlag = &cli.StringFlag{
		Name:      "initcode",
		Usage:     "desired initcode to deploy at vanity address",
		Value:     "0x",
		Validator: checkHex,
		Required:  true,
	}
	gasLimitFlag = &cli.UintFlag{
		Name:  "gaslimit",
		Usage: "desired gas limit for deployment transaction",
		Value: 250000,
	}
	gasPriceFlag = &cli.UintFlag{
		Name:  "gasprice",
		Usage: "desired gas price (gwei) for deployment transaction",
		Value: 1000,
	}

	app = &cli.Command{
		Name:  "nick",
		Usage: "a vanity address searcher for deployments using nick's method",
		Commands: []*cli.Command{
			{
				Name:  "search",
				Usage: "Search for a vanity address to deploy a contract using nicks method.",
				Flags: []cli.Flag{threadsFlag, scoreFlag, prefixFlag, suffixFlag,
					initcodeFlag, gasLimitFlag, gasPriceFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					f := newTask(cmd)
					return f.run(f.brute)
				},
			},
			{
				Name:  "create2",
				Usage: "Search for a CREATE2 salt giving a vanity address when deploying through Arachnid's deployment proxy.",
				Flags: []cli.Flag{threadsFlag, scoreFlag, prefixFlag, suffixFlag,
					initcodeFlag, gasLimitFlag, gasPriceFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					f := newTask(cmd)
					return f.run(f.bruteCreate2)
				},
			},
			{
				Name:      "print",
				Usage:     "Print reads a json tx object from file and prints the deployment info.",
				ArgsUsage: "[filename]",
				Action:    print,
			},
		},
	}
)

// create2Factory is Arachnid's CREATE2 deployment proxy, deployed at the same
// address on mainnet and most other chains.
// See https://github.com/Arachnid/deterministic-deployment-proxy.
var create2Factory = common.HexToAddress("0x4e59b44847b379578588920cA78FbF26c0B4956C")

func newTask(cmd *cli.Command) *task {
	return &task{
		prefix:    common.FromHex(cmd.String(prefixFlag.Name)),
		suffix:    common.FromHex(cmd.String(suffixFlag.Name)),
		initcode:  common.FromHex(cmd.String(initcodeFlag.Name)),
		gasLimit:  cmd.Uint(gasLimitFlag.Name),
		gasPrice:  cmd.Uint(gasPriceFlag.Name),
		threads:   cmd.Int(threadsFlag.Name),
		score:     int(cmd.Int(scoreFlag.Name)),
		highscore: &atomic.Uint64{},
		count:     &atomic.Uint64{},
		quit:      make(chan struct{}),
	}
}

func main() {
	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

// task represents a search for a vanity deployment address.
type task struct {
	prefix   []byte
	suffix   []byte
	initcode []byte

	gasLimit uint64
	gasPrice uint64

	threads int64
	score   int

	highscore *atomic.Uint64
	count     *atomic.Uint64
	quit      chan struct{}
}

func (f *task) run(brute func()) error {
	go func() {
		logTime := time.Now()
		for {
			if time.Since(logTime) > time.Second*30 {
				fmt.Printf("Did %d attempts in %v, best score is %d\n", f.count.Load(), time.Since(logTime), f.highscore.Load())
				logTime = time.Now()
				f.count.Store(0)
			}
		}
	}()

	// Spin up workers.
	for i := 0; i < int(f.threads); i++ {
		go brute()
	}

	<-f.quit
	return nil
}

// brute runs the brute force seacher on a single thread.
func (t *task) brute() {
	var (
		inner = types.LegacyTx{
			Nonce:    0,
			GasPrice: newGwei(t.gasPrice),
			Gas:      t.gasLimit,
			To:       nil,
			Value:    big.NewInt(0),
			Data:     t.initcode,
			V:        big.NewInt(27),
			R:        big.NewInt(0x539),
			S:        big.NewInt(0x1337),
		}
		hash = sighash(types.NewTx(&inner))
		u64  = make([]byte, 8)
	)
	for {
		sender, err := recoverPlain(hash, inner.R, inner.S, inner.V)
		if err != nil {
			panic(err)
		}
		addr := crypto.CreateAddress(sender, 0)

		if bytes.Equal(addr[len(addr)-len(t.suffix):], t.suffix) {
			score := compare(t.prefix, addr[:]) + len(t.suffix)*2
			if uint64(score) > t.highscore.Load() {
				t.highscore.Store(uint64(score))
			}
			if score >= t.score {
				tx := types.NewTx(&inner)
				txjson, _ := json.MarshalIndent(tx, "", "  ")
				fmt.Printf("New highscore: %d\nSender: %v\nAddress: %v\nTx:\n%v\n", score, sender, addr, string(txjson))
			}
		}
		crand.Read(u64)
		inner.S = new(big.Int).SetUint64(binary.BigEndian.Uint64(u64))
		t.count.Add(1)
	}
}

// bruteCreate2 runs the brute force salt searcher on a single thread. It
// searches for a salt value such that a deployment of the initcode through
// the CREATE2 factory lands on a vanity address.
func (t *task) bruteCreate2() {
	var (
		hasher = sha3.NewLegacyKeccak256().(crypto.KeccakState)
		buf    = create2Preimage(create2Factory, crypto.Keccak256Hash(t.initcode))
		salt   = buf[21:53]
		hash   common.Hash
	)
	// Start each thread at a random salt to avoid overlapping search ranges.
	crand.Read(salt)

	for {
		hasher.Reset()
		hasher.Write(buf[:])
		hasher.Read(hash[:])
		addr := hash[12:]

		if bytes.Equal(addr[len(addr)-len(t.suffix):], t.suffix) {
			score := compare(t.prefix, addr) + len(t.suffix)*2
			if uint64(score) > t.highscore.Load() {
				t.highscore.Store(uint64(score))
			}
			if score >= t.score {
				t.reportCreate2(score, salt, common.BytesToAddress(addr))
			}
		}
		// Increment the salt.
		for i := len(salt) - 1; i >= 0; i-- {
			salt[i]++
			if salt[i] != 0 {
				break
			}
		}
		t.count.Add(1)
	}
}

// create2Preimage assembles the CREATE2 address preimage
// 0xff ++ deployer ++ salt ++ initcodeHash with a zero salt.
func create2Preimage(deployer common.Address, initcodeHash common.Hash) [85]byte {
	var buf [85]byte
	buf[0] = 0xff
	copy(buf[1:21], deployer[:])
	copy(buf[53:85], initcodeHash[:])
	return buf
}

// reportCreate2 prints a found salt along with the unsigned deployment
// transaction against the CREATE2 factory.
func (t *task) reportCreate2(score int, salt []byte, addr common.Address) {
	// The factory expects the salt followed by the initcode as calldata.
	data := make([]byte, 0, 32+len(t.initcode))
	data = append(data, salt...)
	data = append(data, t.initcode...)
	to := create2Factory
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    0,
		GasPrice: newGwei(t.gasPrice),
		Gas:      t.gasLimit,
		To:       &to,
		Value:    big.NewInt(0),
		Data:     data,
	})
	txjson, _ := json.MarshalIndent(tx, "", "  ")
	fmt.Printf("New highscore: %d\nSalt: %v\nAddress: %v\nTx (unsigned):\n%v\n",
		score, hexutil.Encode(salt), addr, string(txjson))
}

// print recomputes the deployer and deployment address from a tx json and
// prints the result.
func print(ctx context.Context, cmd *cli.Command) error {
	b, err := os.ReadFile(cmd.Args().First())
	if err != nil {
		return fmt.Errorf("unable to read file: %w", err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalJSON(b); err != nil {
		return fmt.Errorf("unable to parse tx: %w", err)
	}
	if to := tx.To(); to != nil && *to == create2Factory {
		data := tx.Data()
		if len(data) < 32 {
			return fmt.Errorf("tx data too short for a create2 deployment: %d bytes", len(data))
		}
		salt := common.BytesToHash(data[:32])
		addr := crypto.CreateAddress2(create2Factory, salt, crypto.Keccak256(data[32:]))
		fmt.Printf("Factory: %v\nSalt: %v\nAddress: %v\n", create2Factory, salt, addr)
		return nil
	}
	signer := types.LatestSignerForChainID(common.Big1)
	sender, err := signer.Sender(&tx)
	if err != nil {
		return fmt.Errorf("failed to recover tx sender: %w", err)
	}
	addr := crypto.CreateAddress(sender, 0)
	fmt.Printf("Sender: %v\nAddress: %v", sender, addr)
	return nil
}

// checkHex verifies the string is a proper hex value.
func checkHex(s string) error {
	if _, err := hexutil.Decode(s); err != nil {
		return fmt.Errorf("flag value must be hex: got=%v err=%v", s, err)
	}
	return nil
}

// compare returns the number of matching nibbles across a and b.
func compare(a, b []byte) int {
	for i, x := range a {
		y := b[i]
		if (x & 0xf0) != (y & 0xf0) {
			return 2 * i
		}
		if (x & 0xf) != (y & 0xf) {
			return 2*i + 1
		}
	}
	return 2 * len(a)
}

func newGwei(n uint64) *big.Int {
	return new(big.Int).Mul(big.NewInt(int64(n)), big.NewInt(params.GWei))
}

// sighash computes the hash which will be signed over.
func sighash(tx *types.Transaction) common.Hash {
	return rlpHash([]interface{}{
		tx.Nonce(),
		tx.GasPrice(),
		tx.Gas(),
		tx.To(),
		tx.Value(),
		tx.Data(),
	})
}

func recoverPlain(sighash common.Hash, R, S, Vb *big.Int) (common.Address, error) {
	V := byte(Vb.Uint64() - 27)

	// encode the signature in uncompressed format
	r, s := R.Bytes(), S.Bytes()
	sig := make([]byte, crypto.SignatureLength)
	copy(sig[32-len(r):32], r)
	copy(sig[64-len(s):64], s)
	sig[64] = V

	// recover the public key from the signature
	pub, err := crypto.Ecrecover(sighash[:], sig)
	if err != nil {
		return common.Address{}, err
	}

	var addr common.Address
	copy(addr[:], crypto.Keccak256(pub[1:])[12:])
	return addr, nil
}

// hasherPool holds LegacyKeccak256 hashers for rlpHash.
var hasherPool = sync.Pool{
	New: func() interface{} { return sha3.NewLegacyKeccak256() },
}

// encodeBufferPool holds temporary encoder buffers for DeriveSha and TX encoding.
var encodeBufferPool = sync.Pool{
	New: func() interface{} { return new(bytes.Buffer) },
}

// rlpHash encodes x and hashes the encoded bytes.
func rlpHash(x interface{}) (h common.Hash) {
	sha := hasherPool.Get().(crypto.KeccakState)
	defer hasherPool.Put(sha)
	sha.Reset()
	rlp.Encode(sha, x)
	sha.Read(h[:])
	return h
}
