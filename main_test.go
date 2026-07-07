package main

import (
	"bytes"
	crand "crypto/rand"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/sha3"
)

func TestCompare(t *testing.T) {
	start, end := common.FromHex("0x0000000000"), common.FromHex("0xbeac02")
	have := common.FromHex("0xffffffffffffffffffffffffffffffffffbeac02")
	score := compare(end, have[len(have)-len(end):])
	if score != 6 {
		t.Fatalf("expected score 6, got %d", score)
	}
	score = compare(start, have[:])
	if score != 0 {
		t.Fatalf("expected score 0, got %d", score)
	}
}

// TestCreate2Preimage checks that hashing the preimage buffer used by the
// search loop yields the same address as the reference implementation.
func TestCreate2Preimage(t *testing.T) {
	initcode := common.FromHex("0x60425000")
	buf := create2Preimage(create2Factory, crypto.Keccak256Hash(initcode))
	salt := buf[21:53]
	for i := 0; i < 10; i++ {
		crand.Read(salt)
		hash := crypto.Keccak256(buf[:])
		want := crypto.CreateAddress2(create2Factory, common.BytesToHash(salt), crypto.Keccak256(initcode))
		if !bytes.Equal(hash[12:], want[:]) {
			t.Fatalf("address mismatch: have %x, want %v", hash[12:], want)
		}
	}
}

// BenchmarkCreate2Hash measures a single attempt of the create2 search loop.
func BenchmarkCreate2Hash(b *testing.B) {
	var (
		hasher = sha3.NewLegacyKeccak256().(crypto.KeccakState)
		buf    = create2Preimage(create2Factory, crypto.Keccak256Hash(common.FromHex("0x60425000")))
		hash   common.Hash
	)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hasher.Reset()
		hasher.Write(buf[:])
		hasher.Read(hash[:])
	}
}
