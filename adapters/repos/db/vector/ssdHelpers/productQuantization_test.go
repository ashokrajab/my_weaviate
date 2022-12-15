package ssdhelpers_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	ssdhelpers "github.com/semi-technologies/weaviate/adapters/repos/db/vector/ssdHelpers"
	testinghelpers "github.com/semi-technologies/weaviate/adapters/repos/db/vector/testingHelpers"
	"github.com/stretchr/testify/assert"
)

func compare(x []byte, y []byte) bool {
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func TestPQ(t *testing.T) {
	rand.Seed(0)
	dimensions := 960
	vectors_size := 1000000
	queries_size := 100
	k := 100
	//vectors, queries := testinghelpers.RandomVecs(vectors_size, queries_size, dimensions)
	vectors, queries := testinghelpers.ReadVecs(vectors_size, queries_size, dimensions, "gist", "../diskAnn/testdata")
	testinghelpers.Normalize(vectors)
	testinghelpers.Normalize(queries)

	pq := ssdhelpers.NewProductQuantizer(
		dimensions,
		256,
		ssdhelpers.NewCosineDistanceProvider(),
		func(ctx context.Context, id uint64) ([]float32, error) {
			return vectors[int(id)], nil
		},
		dimensions,
		vectors_size,
		ssdhelpers.UseTileEncoder,
	)
	before := time.Now()
	pq.Fit()
	fmt.Println("time elapse:", time.Since(before))
	before = time.Now()
	encoded := make([][]byte, vectors_size)
	for i := 0; i < vectors_size; i++ {
		encoded[i] = pq.Encode(vectors[i])
	}
	fmt.Println("time elapse:", time.Since(before))
	fmt.Println("=============")
	s := ssdhelpers.NewSortedSet(
		k,
		func(ctx context.Context, id uint64) ([]float32, error) {
			return vectors[int(id)], nil
		},
		ssdhelpers.NewCosineDistanceProvider(),
		nil,
	)
	s.SetPQ(encoded, pq)
	var relevant uint64
	truths := testinghelpers.BuildTruths(queries_size, vectors_size, queries, vectors, k, ssdhelpers.NewCosineDistanceProvider().Distance, "../diskAnn/testdata/gist/cosine")
	queries_size = 100
	for i, query := range queries {
		if i == queries_size {
			break
		}
		pq.CenterAt(query)
		s.ReCenter(query, false)
		for v := range vectors {
			s.AddPQVector(uint64(v), nil, nil)
		}
		results, _ := s.Elements(k)
		relevant += testinghelpers.MatchesInLists(truths[i], results)
	}
	recall := float32(relevant) / float32(k*queries_size)
	fmt.Println(recall)
	assert.True(t, recall > 0.65)
}

func TestPQSift(t *testing.T) {
	rand.Seed(0)
	dimensions := 128
	vectors_size := 99500
	queries_size := 500
	k := 10
	sift_vectors := testinghelpers.ReadSiftVecsFrom("../../../../../test/benchmark/sift/sift_base.fvecs", vectors_size+queries_size, dimensions)
	vectors := sift_vectors[:vectors_size]
	queries := sift_vectors[vectors_size : vectors_size+queries_size]
	fmt.Printf("len vectors: %d\n", len(vectors))
	fmt.Printf("len queries: %d\n", len(queries))

	before := time.Now()
	pq := ssdhelpers.NewProductQuantizer(
		128,
		256,
		ssdhelpers.NewL2DistanceProvider(),
		func(ctx context.Context, id uint64) ([]float32, error) {
			return vectors[int(id)], nil
		},
		dimensions,
		vectors_size,
		ssdhelpers.UseTileEncoder,
	)
	pq.Fit()
	encoded := make([][]byte, vectors_size)
	for i := 0; i < vectors_size; i++ {
		encoded[i] = pq.Encode(vectors[i])
	}
	fmt.Println("time elapse (fit and encode):", time.Since(before))
	fmt.Println("=============")
	s := ssdhelpers.NewSortedSet(
		k,
		func(ctx context.Context, id uint64) ([]float32, error) {
			return vectors[int(id)], nil
		},
		ssdhelpers.NewL2DistanceProvider(),
		nil,
	)
	s.SetPQ(encoded, pq)
	var relevant uint64
	for _, query := range queries {
		pq.CenterAt(query)
		truth := testinghelpers.BruteForce(vectors, query, k, ssdhelpers.NewL2DistanceProvider().Distance)
		s.ReCenter(query, false)
		for v := range vectors {
			s.AddPQVector(uint64(v), nil, nil)
		}
		results, _ := s.Elements(k)
		relevant += testinghelpers.MatchesInLists(truth, results)
	}
	recall := float32(relevant) / float32(k*queries_size)
	fmt.Println(recall)
	assert.True(t, recall > 0.65)
}

func TestEncodeDecodeSift(t *testing.T) {
	rand.Seed(0)
	dimensions := 128
	vectors_size := 100000
	queries_size := 100
	k := 10
	vectors, queries := testinghelpers.ReadVecs(vectors_size, queries_size, dimensions, "sift", "../diskAnn/testdata")
	truths := testinghelpers.BuildTruths(queries_size, vectors_size, queries, vectors, k, ssdhelpers.NewL2DistanceProvider().Distance, "../diskAnn/testdata")
	fmt.Printf("len vectors: %d\n", len(vectors))
	fmt.Printf("len queries: %d\n", len(queries))

	before := time.Now()
	pq := ssdhelpers.NewProductQuantizer(
		128,
		256,
		ssdhelpers.NewL2DistanceProvider(),
		func(ctx context.Context, id uint64) ([]float32, error) {
			return vectors[int(id)], nil
		},
		dimensions,
		vectors_size,
		ssdhelpers.UseTileEncoder,
	)
	pq.Fit()
	distorted := make([][]float32, vectors_size)
	for i := 0; i < vectors_size; i++ {
		distorted[i] = pq.Decode(pq.Encode(vectors[i]))
	}
	fmt.Println("time elapse (fit and encode):", time.Since(before))
	fmt.Println("=============")
	s := ssdhelpers.NewSortedSet(
		k,
		func(ctx context.Context, id uint64) ([]float32, error) {
			return distorted[int(id)], nil
		},
		ssdhelpers.NewL2DistanceProvider(),
		nil,
	)
	var relevant uint64
	for i, query := range queries {
		s.ReCenter(query, false)
		for v := range vectors {
			s.Add(uint64(v))
		}
		results, _ := s.Elements(k)
		relevant += testinghelpers.MatchesInLists(truths[i], results)
	}
	recall := float32(relevant) / float32(k*queries_size)
	fmt.Println(recall)
	assert.True(t, recall > 0.65)
}

/*
func TestPQDistance(t *testing.T) {
	rand.Seed(0)
	dimensions := 128
	vectors_size := 1000
	queries_size := 100

	vectors, queries := testinghelpers.RandomVecs(vectors_size, queries_size, dimensions)
	pq := ssdhelpers.NewProductQuantizer(
		32,
		256,
		ssdhelpers.L2,
		func(ctx context.Context, id uint64) ([]float32, error) {
			return vectors[int(id)], nil
		},
		dimensions,
		vectors_size,
	)
	before := time.Now()
	pq.Fit()
	fmt.Println("time elapse:", time.Since(before))

	encoded := make([][]byte, vectors_size)
	for i := 0; i < vectors_size; i++ {
		encoded[i] = pq.Encode(vectors[i])
	}

	for _, q := range queries {
		pq.CenterAt(q)
		for _, e2 := range encoded {
			assert.Equal(t, ssdhelpers.L2(q, pq.Decode(e2)), pq.Distance(e2))
			break
		}

	}
}
*/
