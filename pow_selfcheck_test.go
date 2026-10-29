package main

import (
	"encoding/hex"
	"testing"
)

func TestKeccakPermutationCorrectness(t *testing.T) {
	cases := []struct {
		name string
		pad  byte
		want string
	}{
		{"sha3-256(empty)", 0x06, "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
		{"keccak-256(empty)", 0x01, "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"},
		{"sha3-256(abc)", 0x06, "3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532"},
	}
	for _, c := range cases {
		msg := []byte{}
		if c.name == "sha3-256(abc)" {
			msg = []byte("abc")
		}
		got := keccakGeneric(msg, c.pad, 0, 24)
		if hex.EncodeToString(got[:]) != c.want {
			t.Fatalf("%s: got %x want %s", c.name, got, c.want)
		}
	}
}

func TestDeepSeekPowRoundTrip(t *testing.T) {
	challenge := deepseekHash([]byte("624ed108470091075858_1793302850901_12345"))
	n, ok := solveDeepSeekPowV1(hex.EncodeToString(challenge[:]), "624ed108470091075858", 1793302850901, 1000000)
	if !ok || n != 12345 {
		t.Fatalf("PoW 自解失败: n=%d ok=%v", n, ok)
	}
}
