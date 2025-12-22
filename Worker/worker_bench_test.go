package main

import (
	"fmt"
	"testing"

	pb "cracker/cracker"
)

func BenchmarkHashCandidateMD5(b *testing.B) {
	candidate := "password123"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hashCandidate(pb.HashMode_HASH_MODE_MD5, candidate)
	}
}

func BenchmarkHashCandidateSHA256(b *testing.B) {
	candidate := "password123"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hashCandidate(pb.HashMode_HASH_MODE_SHA256, candidate)
	}
}

func BenchmarkHashCandidateVaryingInput(b *testing.B) {
	base := "candidate-"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		candidate := fmt.Sprintf("%s%d", base, i)
		_ = hashCandidate(pb.HashMode_HASH_MODE_MD5, candidate)
	}
}
