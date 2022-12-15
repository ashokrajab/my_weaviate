package ssdhelpers

import (
	"context"
	"encoding/gob"
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"

	"github.com/pkg/errors"
)

type Encoder int

const (
	UseTileEncoder   Encoder = 0
	UseKMeansEncoder         = 1
)

type DistanceLookUpTable struct {
	distances [][]float32
	center    []float32
}

func NewDistanceLookUpTable(segments int, centroids int, center []float32) *DistanceLookUpTable {
	distances := make([][]float32, segments)
	for m := 0; m < segments; m++ {
		distances[m] = make([]float32, centroids)
	}

	return &DistanceLookUpTable{
		distances: distances,
		center:    center,
	}
}

func (lut *DistanceLookUpTable) LookUp(
	encoded []byte,
	distance func(segment int, b byte, center []float32) float32,
	aggregate func(current float32, x float32) float32,
) float32 {
	d := lut.distances[0][encoded[0]]
	if d == 0 {
		d = distance(0, encoded[0], lut.center)
		lut.distances[0][encoded[0]] = d
	}
	dist := d

	for i := 1; i < len(encoded); i++ {
		b := encoded[i]
		d = lut.distances[i][b]
		if d == 0 {
			d = distance(i, b, lut.center)
			lut.distances[i][b] = d
		}
		dist = aggregate(dist, d)
	}
	return dist
}

type ProductQuantizer struct {
	ks               int
	m                int
	ds               int
	distance         DistanceProvider
	vectorForIDThunk VectorForID
	dimensions       int
	dataSize         int
	kms              []NoopEncoder
	encoderType      Encoder
}

type PQData struct {
	Ks          int
	M           int
	Dimensions  int
	DataSize    int
	EncoderType Encoder
}

type NoopEncoder interface {
	ToDisk(path string, id int)
	Encode(x []float32) byte
	Centroid(b byte) []float32
	Add(x float32)
	Fit() error
}

const PQDataFileName = "pq.gob"

type PQConfig struct {
	Size             int
	Segments         int
	Centroids        int
	Distance         DistanceProvider
	VectorForIDThunk VectorForID
	Dimensions       int
	DataSize         int
	EncoderType      Encoder
}

func NewProductQuantizer(segments int, centroids int, distance DistanceProvider, vectorForIDThunk VectorForID, dimensions int, dataSize int, encoderType Encoder) *ProductQuantizer {
	if dataSize == 0 {
		panic("data must not be empty")
	}
	if dimensions%segments != 0 {
		panic("dimension must be a multiple of m")
	}
	pq := &ProductQuantizer{
		ks:               centroids,
		m:                segments,
		ds:               dimensions / segments,
		distance:         distance,
		vectorForIDThunk: vectorForIDThunk,
		dimensions:       dimensions,
		dataSize:         dataSize,
		encoderType:      encoderType,
	}
	return pq
}

func (pq *ProductQuantizer) ToDisk(path string) {
	if pq == nil {
		return
	}
	fData, err := os.Create(fmt.Sprintf("%s/%s", path, PQDataFileName))
	if err != nil {
		panic(errors.Wrap(err, "Could not create kmeans file"))
	}
	defer fData.Close()

	dEnc := gob.NewEncoder(fData)
	err = dEnc.Encode(PQData{
		Ks:          pq.ks,
		M:           pq.m,
		Dimensions:  pq.dimensions,
		DataSize:    pq.dataSize,
		EncoderType: pq.encoderType,
	})
	if err != nil {
		panic(errors.Wrap(err, "Could not encode pq"))
	}
	for id, km := range pq.kms {
		km.ToDisk(path, id)
	}
}

func PQFromDisk(path string, VectorForIDThunk VectorForID, distance DistanceProvider) *ProductQuantizer {
	fData, err := os.Open(fmt.Sprintf("%s/%s", path, PQDataFileName))
	if err != nil {
		return nil
	}
	defer fData.Close()

	data := PQData{}
	dDec := gob.NewDecoder(fData)
	err = dDec.Decode(&data)
	if err != nil {
		panic(errors.Wrap(err, "Could not decode data"))
	}
	pq := NewProductQuantizer(data.M, data.Ks, distance, VectorForIDThunk, data.Dimensions, data.DataSize, data.EncoderType)
	switch data.EncoderType {
	case UseKMeansEncoder:
		pq.kms = make([]NoopEncoder, pq.m)
		for id := range pq.kms {
			pq.kms[id] = KMeansFromDisk(path, id, VectorForIDThunk, distance.Distance)
		}
	case UseTileEncoder:
		pq.kms = make([]NoopEncoder, pq.m)
		for id := range pq.kms {
			pq.kms[id] = TileEncoderFromDisk(path, id)
		}
	}
	return pq
}

func (pq *ProductQuantizer) extractSegment(i int, v []float32) []float32 {
	return v[i*pq.ds : (i+1)*pq.ds]
}

type Action func(workerId uint64, taskIndex uint64, mutex *sync.Mutex)

func concurrently(n uint64, action Action) {
	n64 := float64(n)
	workerCount := runtime.GOMAXPROCS(0)
	mutex := &sync.Mutex{}
	wg := &sync.WaitGroup{}
	split := uint64(math.Ceil(n64 / float64(workerCount)))
	for worker := uint64(0); worker < uint64(workerCount); worker++ {
		wg.Add(1)
		go func(workerID uint64) {
			defer wg.Done()
			for i := workerID * split; i < uint64(math.Min(float64((workerID+1)*split), n64)); i++ {
				action(workerID, i, mutex)
			}
		}(worker)
	}
	wg.Wait()
}

func (pq *ProductQuantizer) Fit() {
	switch pq.encoderType {
	case UseTileEncoder:
		pq.kms = make([]NoopEncoder, pq.m)
		concurrently(uint64(pq.m), func(_ uint64, i uint64, _ *sync.Mutex) {
			pq.kms[i] = NewTileEncoder(int(math.Log2(float64(pq.ks))))
			for j := 0; j < pq.dataSize; j++ {
				vec, _ := pq.vectorForIDThunk(context.Background(), uint64(j))
				pq.kms[i].Add(vec[i])
			}
		})
	case UseKMeansEncoder:
		pq.kms = make([]NoopEncoder, pq.m)
		concurrently(uint64(pq.m), func(_ uint64, i uint64, _ *sync.Mutex) {
			pq.kms[i] = NewKMeans(
				pq.ks,
				pq.distance.Distance,
				func(ctx context.Context, id uint64) ([]float32, error) {
					v, e := pq.vectorForIDThunk(ctx, id)
					return pq.extractSegment(int(i), v), e
				},
				pq.dataSize,
				pq.ds)
			err := pq.kms[i].Fit()
			if err != nil {
				panic(err)
			}
		})
	}
	/*for i := 0; i < 1; i++ {
		fmt.Println("********")
		centers := make([]float64, 0)
		for c := 0; c < pq.ks; c++ {
			centers = append(centers, float64(pq.kms[i].Centroid(byte(c))[0]))
		}
		hist := histogram.Hist(60, centers)
		histogram.Fprint(os.Stdout, hist, histogram.Linear(5))
	}*/
}

func (pq *ProductQuantizer) Encode(vec []float32) []byte {
	codes := make([]byte, pq.m)
	for i := 0; i < pq.m; i++ {
		codes[i] = byte(pq.kms[i].Encode(pq.extractSegment(i, vec)))
	}
	return codes
}

func (pq *ProductQuantizer) Decode(code []byte) []float32 {
	vec := make([]float32, 0, len(code))
	for i, b := range code {
		vec = append(vec, pq.kms[i].Centroid(b)...)
	}
	return vec
}

func (pq *ProductQuantizer) CenterAt(vec []float32) *DistanceLookUpTable {
	return NewDistanceLookUpTable(pq.m, pq.ks, vec)
}

func (pq *ProductQuantizer) DistanceBetweenNodes(x, y []byte) float32 {
	dist := pq.distance.Distance(pq.kms[0].Centroid(x[0]), pq.kms[0].Centroid(y[0]))
	for i := 1; i < len(x); i++ {
		dist = pq.distance.Aggregate(dist, pq.distance.Distance(pq.kms[i].Centroid(x[i]), pq.kms[i].Centroid(y[i])))
	}
	return dist
}

func (pq *ProductQuantizer) DistanceBetweenNodeAndVector(x []float32, encoded []byte) float32 {
	dist := pq.distance.Distance(pq.extractSegment(0, x), pq.kms[0].Centroid(encoded[0]))
	for i := 1; i < len(encoded); i++ {
		b := encoded[i]
		dist = pq.distance.Aggregate(dist, pq.distance.Distance(pq.extractSegment(i, x), pq.kms[i].Centroid(b)))
	}
	return dist
}

func (pq *ProductQuantizer) distanceForSegment(segment int, b byte, center []float32) float32 {
	return pq.distance.Distance(pq.extractSegment(segment, center), pq.kms[segment].Centroid(b))
}

func (pq *ProductQuantizer) Distance(encoded []byte, lut *DistanceLookUpTable) float32 {
	return lut.LookUp(encoded, pq.distanceForSegment, pq.distance.Aggregate)
}
