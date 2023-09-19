// Originally written by @holiman, minor adjustments by me.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"golang.org/x/crypto/sha3"
)

func main() {
	go func() {
		logTime := time.Now()
		for {
			if time.Since(logTime) > time.Second*30 {
				fmt.Printf("Did %d attempts in %v, best score is %d\n", count.Load(), time.Since(logTime), highscore.Load())
				logTime = time.Now()
				count.Store(0)
			}
		}
	}()
	threads := runtime.NumCPU()
	if len(os.Args) == 2 {
		val, _ := strconv.ParseInt(os.Args[1], 10, 64)
		threads = int(val)
	}
	fmt.Println("thread count:", threads)
	for i := 0; i < threads; i++ {
		go brute(i, threads, common.FromHex("0000000000"), common.FromHex("0xbeac02"))
	}
	brute(threads, threads, common.FromHex("0000000000"), common.FromHex("0xbeac02"))
}

var highscore atomic.Int64
var count atomic.Uint64

func brute(idx, threads int, start []byte, end []byte) {
	var (
		inner = types.LegacyTx{
			Nonce:    0,
			GasPrice: newGwei(1000),
			Gas:      250000,
			To:       nil,
			Value:    big.NewInt(0),
			Data:     common.FromHex("0x60618060095f395ff33373fffffffffffffffffffffffffffffffffffffffe14604d57602036146024575f5ffd5b5f35801560495762001fff810690815414603c575f5ffd5b62001fff01545f5260205ff35b5f5ffd5b62001fff42064281555f359062001fff015500"),
			V:        big.NewInt(27),
			R:        big.NewInt(0x539),
			S:        big.NewInt(0x1337 + int64(idx)),
		}
		tx   = types.NewTx(&inner)
		step = big.NewInt(int64(threads))
		hash = sighash(types.NewTx(&inner))
	)
	for {
		sender, err := recoverPlain(hash, inner.R, inner.S, inner.V)
		if err != nil {
			panic(err)
		}
		addr := crypto.CreateAddress(sender, 0)

		score := compare(end, addr[len(addr)-len(end):])
		if score == len(end)*2 {
			score += compare(start, addr[:])
			if int64(score) > highscore.Load() {
				highscore.Store(int64(score))
			}
			if score >= 9 {
				txjson, _ := json.MarshalIndent(tx, "", "  ")
				fmt.Printf("New highscore: %d\nSender: %v\nAddress: %v\nTx:\n%v\n", score, sender, addr, string(txjson))
			}
		}

		inner.S.Add(inner.S, step)
		count.Add(1)
	}
}

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

func newGwei(n int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(n), big.NewInt(params.GWei))
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
