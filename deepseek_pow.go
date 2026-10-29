package main

import (
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"
)

var dsRC = [24]uint64{
	0x0000000000000001, 0x0000000000008082, 0x800000000000808a, 0x8000000080008000,
	0x000000000000808b, 0x0000000080000001, 0x8000000080008081, 0x8000000000008009,
	0x000000000000008a, 0x0000000000000088, 0x0000000080008009, 0x000000008000000a,
	0x000000008000808b, 0x800000000000008b, 0x8000000000008089, 0x8000000000008003,
	0x8000000000008002, 0x8000000000000080, 0x000000000000800a, 0x800000008000000a,
	0x8000000080008081, 0x8000000000008080, 0x0000000080000001, 0x8000000080008008,
}

var dsRho = [5][5]int{
	{0, 36, 3, 41, 18},
	{1, 44, 10, 45, 2},
	{62, 6, 43, 15, 61},
	{28, 55, 25, 21, 56},
	{27, 20, 39, 8, 14},
}

func rotl64(x uint64, n int) uint64 {
	if n == 0 {
		return x
	}
	return (x << uint(n)) | (x >> uint(64-n))
}

func keccakF1600Ds(a *[25]uint64) {
	keccakPermute(a, 1, 24)
}

func keccakPermute(a *[25]uint64, from, to int) {
	var c [5]uint64
	var b [25]uint64
	var d [5]uint64
	for round := from; round < to; round++ {
		for x := 0; x < 5; x++ {
			c[x] = a[x] ^ a[x+5] ^ a[x+10] ^ a[x+15] ^ a[x+20]
		}
		for x := 0; x < 5; x++ {
			d[x] = c[(x+4)%5] ^ rotl64(c[(x+1)%5], 1)
		}
		for y := 0; y < 5; y++ {
			for x := 0; x < 5; x++ {
				a[y*5+x] ^= d[x]
			}
		}
		for y := 0; y < 5; y++ {
			for x := 0; x < 5; x++ {
				b[(((x*3)+(y*2))%5)*5+x] = rotl64(a[x*5+y], dsRho[y][x])
			}
		}
		for y := 0; y < 5; y++ {
			for x := 0; x < 5; x++ {
				idx := y * 5
				a[idx+x] = b[idx+x] ^ (^b[idx+((x+1)%5)] & b[idx+((x+2)%5)])
			}
		}
		a[0] ^= dsRC[round]
	}
}

func dsAbsorbBlock(a *[25]uint64, block []byte) {
	for i := 0; i < 17; i++ {
		a[i] ^= binary.LittleEndian.Uint64(block[i*8 : i*8+8])
	}
	keccakF1600Ds(a)
}

// keccakGeneric 通用版本，便于自检（rounds 24 + pad 0x06 → SHA3-256；24 + 0x01 → Keccak-256）
func keccakGeneric(msg []byte, pad byte, from, to int) [32]byte {
	var a [25]uint64
	offset := 0
	for offset+136 <= len(msg) {
		for i := 0; i < 17; i++ {
			a[i] ^= binary.LittleEndian.Uint64(msg[offset+i*8 : offset+i*8+8])
		}
		keccakPermute(&a, from, to)
		offset += 136
	}
	var last [136]byte
	copy(last[:], msg[offset:])
	last[len(msg)-offset] = pad
	last[135] |= 0x80
	for i := 0; i < 17; i++ {
		a[i] ^= binary.LittleEndian.Uint64(last[i*8 : i*8+8])
	}
	keccakPermute(&a, from, to)
	var out [32]byte
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint64(out[i*8:], a[i])
	}
	return out
}

// deepseekHash 复刻原版 APK 的 DeepSeekHashV1：Keccak-f1600（23 轮）· rate 136 · 0x06/0x80 padding
func deepseekHash(msg []byte) [32]byte {
	var a [25]uint64
	offset := 0
	for offset+136 <= len(msg) {
		dsAbsorbBlock(&a, msg[offset:offset+136])
		offset += 136
	}
	var last [136]byte
	copy(last[:], msg[offset:])
	last[len(msg)-offset] = 0x06
	last[135] |= 0x80
	dsAbsorbBlock(&a, last[:])
	var out [32]byte
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint64(out[i*8:], a[i])
	}
	return out
}

// solveDeepSeekPowV1 找满足 hash(salt_expire_at_ + n) == challenge 的 n
func solveDeepSeekPowV1(challengeHex, salt string, expireAt, limit int64) (int64, bool) {
	target, err := hex.DecodeString(strings.TrimSpace(challengeHex))
	if err != nil || len(target) != 32 {
		return 0, false
	}
	if limit <= 0 {
		limit = 144000
	}
	prefix := []byte(salt + "_" + strconv.FormatInt(expireAt, 10) + "_")
	buf := make([]byte, len(prefix), len(prefix)+20)
	copy(buf, prefix)
	baseLen := len(prefix)
	var want [32]byte
	copy(want[:], target)
	for n := int64(0); n < limit; n++ {
		buf = append(buf[:baseLen], strconv.FormatInt(n, 10)...)
		if deepseekHash(buf) == want {
			return n, true
		}
	}
	return 0, false
}
