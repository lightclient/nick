package main

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
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
