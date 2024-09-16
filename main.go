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

	sigRFlag = &cli.StringFlag{
		Name:        "sig-r",
		Usage:       "R value of the transaction signature",
		Value:       "0x0539",
		DefaultText: "0x0539",
		Validator:   checkHex,
	}
	sigSFlag = &cli.StringFlag{
		Name:        "sig-s",
		Usage:       "S value of the transaction signature",
		Value:       "0x1337",
		DefaultText: "0x1337",
		Validator:   checkHex,
	}

	app = &cli.Command{
		Name:  "nick",
		Usage: "a vanity address searcher for deployments using nick's method",
		Commands: []*cli.Command{
			{
				Name:  "search",
				Usage: "Search for a vanity address to deploy a contract using nicks method.",
				Flags: []cli.Flag{threadsFlag, scoreFlag, prefixFlag, suffixFlag,
					initcodeFlag, gasLimitFlag, gasPriceFlag, sigRFlag, sigSFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					f := task{
						prefix:    common.FromHex(cmd.String(prefixFlag.Name)),
						suffix:    common.FromHex(cmd.String(suffixFlag.Name)),
						initcode:  common.FromHex(cmd.String(initcodeFlag.Name)),
						sigR:      new(big.Int).SetBytes(common.FromHex(cmd.String(sigRFlag.Name))),
						sigS:      new(big.Int).SetBytes(common.FromHex(cmd.String(sigSFlag.Name))),
						gasLimit:  cmd.Uint(gasLimitFlag.Name),
						gasPrice:  cmd.Uint(gasPriceFlag.Name),
						threads:   cmd.Int(threadsFlag.Name),
						score:     int(cmd.Int(scoreFlag.Name)),
						highscore: &atomic.Uint64{},
						count:     &atomic.Uint64{},
						quit:      make(chan struct{}),
					}
					return f.run()
				},
			},
			{
				Name:  "build",
				Usage: "Build a json tx object and prints the deployment info.",
				Flags: []cli.Flag{threadsFlag, scoreFlag, prefixFlag, suffixFlag,
					initcodeFlag, gasLimitFlag, gasPriceFlag, sigRFlag, sigSFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					f := task{
						prefix:    common.FromHex(cmd.String(prefixFlag.Name)),
						suffix:    common.FromHex(cmd.String(suffixFlag.Name)),
						initcode:  common.FromHex(cmd.String(initcodeFlag.Name)),
						sigR:      new(big.Int).SetBytes(common.FromHex(cmd.String(sigRFlag.Name))),
						sigS:      new(big.Int).SetBytes(common.FromHex(cmd.String(sigSFlag.Name))),
						gasLimit:  cmd.Uint(gasLimitFlag.Name),
						gasPrice:  cmd.Uint(gasPriceFlag.Name),
						threads:   cmd.Int(threadsFlag.Name),
						score:     int(cmd.Int(scoreFlag.Name)),
						highscore: &atomic.Uint64{},
						count:     &atomic.Uint64{},
						quit:      make(chan struct{}),
					}
					return f.buildTx()
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

	sigR *big.Int
	sigS *big.Int

	gasLimit uint64
	gasPrice uint64

	threads int64
	score   int

	highscore *atomic.Uint64
	count     *atomic.Uint64
	quit      chan struct{}
}

func (f *task) run() error {
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
		go f.brute()
	}

	<-f.quit
	return nil
}

func (t *task) buildTx() error {
	var (
		inner = types.LegacyTx{
			Nonce:    0,
			GasPrice: newGwei(t.gasPrice),
			Gas:      t.gasLimit,
			To:       nil,
			Value:    big.NewInt(0),
			Data:     t.initcode,
			V:        big.NewInt(27),
			R:        big.NewInt(0).Set(t.sigR),
			S:        big.NewInt(0).Set(t.sigS),
		}
		tx   = types.NewTx(&inner)
		hash = sighash(tx)
	)

	txJson, err := tx.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal json tx: %w", err)
	}

	txRaw, err := tx.MarshalBinary()
	if err != nil {
		return fmt.Errorf("failed to marshal raw tx: %w", err)
	}

	sender, err := recoverPlain(hash, inner.R, inner.S, inner.V)
	if err != nil {
		panic(err)
	}
	addr := crypto.CreateAddress(sender, 0)

	fmt.Printf("TX: %v\n", string(txJson))
	fmt.Printf("RawTX: 0x%x\n", txRaw)
	fmt.Printf("\n")
	fmt.Printf("Sig Hash: %v\n", hash.String())
	fmt.Printf("TX Hash: %v\n", tx.Hash())
	fmt.Printf("\n")
	fmt.Printf("Sender: %v\n", sender.String())
	fmt.Printf("Address: %v\n", addr.String())

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
			R:        big.NewInt(0).Set(t.sigR),
			S:        big.NewInt(0).Set(t.sigS),
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

	//fmt.Printf("sig: 0x%x\n", sig)

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
