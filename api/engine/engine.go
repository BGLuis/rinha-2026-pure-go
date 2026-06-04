package engine

import (
	"math"
	"os"
	"syscall"
	"unsafe"
)

var (
	mmapPtr     []byte
	numClusters int
	centroids   []int16
	bboxes      []int16
	offsets     []uint32
	numBlocks   []uint32

	superBboxes []int16
	superIndex  [][]int
)

func InitEngine(path string) int32 {
	f, err := os.Open(path)
	if err != nil {
		return -2
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return -3
	}
	size := stat.Size()

	mmapPtr, err = syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED|syscall.MAP_POPULATE)
	if err != nil {
		return -4
	}

	header := (*[16]uint32)(unsafe.Pointer(&mmapPtr[0]))
	if header[0] != 0x4E495452 {
		return -5
	}
	if header[1] != 5 {
		return -6
	}

	k := int(header[2])
	numClusters = k

	offset := 64
	centroids = (*[1 << 30]int16)(unsafe.Pointer(&mmapPtr[offset]))[: k*16 : k*16]
	offset += k * 16 * 2

	bboxes = (*[1 << 30]int16)(unsafe.Pointer(&mmapPtr[offset]))[: k*32 : k*32]
	offset += k * 32 * 2

	offsets = (*[1 << 30]uint32)(unsafe.Pointer(&mmapPtr[offset]))[:k:k]
	offset += k * 4

	numBlocks = (*[1 << 30]uint32)(unsafe.Pointer(&mmapPtr[offset]))[:k:k]

	// Super BBoxes
	numSuper := 91
	superCentroids := make([][]int16, numSuper)
	for i := 0; i < numSuper; i++ {
		superCentroids[i] = make([]int16, 16)
		copy(superCentroids[i], centroids[(i*(k/numSuper))*16:])
	}

	assignment := make([]int, k)
	for iter := 0; iter < 5; iter++ {
		sums := make([][]int32, numSuper)
		for i := range sums {
			sums[i] = make([]int32, 16)
		}
		counts := make([]int, numSuper)

		for i := 0; i < k; i++ {
			bestD := int32(math.MaxInt32)
			bestS := 0
			cV := centroids[i*16 : i*16+16]
			for s := 0; s < numSuper; s++ {
				d := dist(cV, superCentroids[s])
				if d < bestD {
					bestD = d
					bestS = s
				}
			}
			assignment[i] = bestS
			counts[bestS]++
			for j := 0; j < 16; j++ {
				sums[bestS][j] += int32(cV[j])
			}
		}

		for s := 0; s < numSuper; s++ {
			if counts[s] > 0 {
				for j := 0; j < 16; j++ {
					superCentroids[s][j] = int16(sums[s][j] / int32(counts[s]))
				}
			}
		}
	}

	superIndex = make([][]int, numSuper)
	for i := 0; i < k; i++ {
		superIndex[assignment[i]] = append(superIndex[assignment[i]], i)
	}

	superBboxes = make([]int16, numSuper*32)
	for s := 0; s < numSuper; s++ {
		if len(superIndex[s]) == 0 {
			continue
		}
		first := superIndex[s][0]
		sBbox := make([]int16, 32)
		copy(sBbox, bboxes[first*32:first*32+32])

		for _, idx := range superIndex[s][1:] {
			cb := bboxes[idx*32 : idx*32+32]
			for j := 0; j < 16; j++ {
				if cb[j] < sBbox[j] {
					sBbox[j] = cb[j]
				}
				if cb[j+16] > sBbox[j+16] {
					sBbox[j+16] = cb[j+16]
				}
			}
		}
		copy(superBboxes[s*32:s*32+32], sBbox)
	}

	// Warmup
	dummyQuery := make([]float32, 14)
	var scratch byte
	for i := 0; i < 1000; i++ {
		SearchVectorFast(&dummyQuery[0], &scratch)
	}

	return 0
}

func dist(q []int16, vec []int16) int32 {
	var sum int32
	for i := 0; i < 16; i++ {
		diff := int32(q[i]) - int32(vec[i])
		sum += diff * diff
	}
	return sum
}

func minDistToBbox(q []int16, bbox []int16) int32 {
	var sum int32
	for i := 0; i < 16; i++ {
		minVal := bbox[i]
		maxVal := bbox[i+16]
		qv := q[i]
		var diff int32
		if qv < minVal {
			diff = int32(minVal - qv)
		} else if qv > maxVal {
			diff = int32(qv - maxVal)
		}
		sum += diff * diff
	}
	return sum
}

func distFast(q *[16]int16, vec *[16]int16) int32 {
	var sum int32
	for i := 0; i < 14; i++ {
		diff := int32(q[i]) - int32(vec[i])
		sum += diff * diff
	}
	return sum
}

func minDistToBboxFast(q *[16]int16, bbox *[32]int16) int32 {
	var sum int32
	for i := 0; i < 14; i++ {
		minVal := bbox[i]
		maxVal := bbox[i+16]
		qv := q[i]
		var diff int32
		if qv < minVal {
			diff = int32(minVal - qv)
		} else if qv > maxVal {
			diff = int32(qv - maxVal)
		}
		sum += diff * diff
	}
	return sum
}

type distIdx struct {
	d   int32
	idx int32
}

func SearchVectorFast(qPtr *float32, scratch *byte) int32 {
	qIn := (*[14]float32)(unsafe.Pointer(qPtr))
	var qI16 [16]int16
	for i := 0; i < 14; i++ {
		qI16[i] = int16(math.Round(float64(qIn[i] * 10000.0)))
	}

	topDists := [5]int32{math.MaxInt32, math.MaxInt32, math.MaxInt32, math.MaxInt32, math.MaxInt32}
	topLabels := [5]uint32{0, 0, 0, 0, 0}

	var superDists [91]distIdx
	for s := int32(0); s < 91; s++ {
		bbox := (*[32]int16)(unsafe.Pointer(&superBboxes[s*32]))
		superDists[s] = distIdx{
			d:   minDistToBboxFast(&qI16, bbox),
			idx: s,
		}
	}

	for i := 1; i < 91; i++ {
		for j := i; j > 0 && superDists[j-1].d > superDists[j].d; j-- {
			superDists[j], superDists[j-1] = superDists[j-1], superDists[j]
		}
	}

	var childDists [4096]distIdx

	for s := 0; s < 91; s++ {
		if superDists[s].d >= topDists[4] {
			break
		}
		sIdx := superDists[s].idx
		count := len(superIndex[sIdx])
		for i := 0; i < count; i++ {
			ki := int32(superIndex[sIdx][i])
			bbox := (*[32]int16)(unsafe.Pointer(&bboxes[ki*32]))
			childDists[i] = distIdx{
				d:   minDistToBboxFast(&qI16, bbox),
				idx: ki,
			}
		}

		for i := 1; i < count; i++ {
			for j := i; j > 0 && childDists[j-1].d > childDists[j].d; j-- {
				childDists[j], childDists[j-1] = childDists[j-1], childDists[j]
			}
		}

		for i := 0; i < count; i++ {
			if childDists[i].d >= topDists[4] {
				break
			}
			ki := childDists[i].idx
			scanClusterFast(ki, &qI16, &topDists, &topLabels)
		}
	}

	var frauds int32
	for i := 0; i < 5; i++ {
		if topDists[i] != math.MaxInt32 && topLabels[i] == 1 {
			frauds++
		}
	}
	return frauds
}

func scanClusterFast(ki int32, q *[16]int16, topDists *[5]int32, topLabels *[5]uint32) {
	offset := int(offsets[ki])
	blocksCount := int(numBlocks[ki])

	ptr := offset

	for b := 0; b < blocksCount; b++ {
		bbox := (*[32]int16)(unsafe.Pointer(&mmapPtr[ptr]))
		numVectors := int(*(*uint32)(unsafe.Pointer(&mmapPtr[ptr+64])))
		ptr += 96

		if minDistToBboxFast(q, bbox) < topDists[4] {
			vPtr := ptr
			for v := 0; v < numVectors; v++ {
				vec := (*[16]int16)(unsafe.Pointer(&mmapPtr[vPtr]))
				d := distFast(q, vec)
				if d < topDists[4] {
					m0 := uint32(uint16(vec[14]))
					m1 := uint32(uint16(vec[15]))
					meta := m0 | (m1 << 16)
					label := meta >> 31
					
					// mask index out to match rust exact behavior just in case
					// idx := meta & 0x7FFFFFFF

					k := 3
					for ; k >= 0 && d < topDists[k]; k-- {
						topDists[k+1] = topDists[k]
						topLabels[k+1] = topLabels[k]
					}
					ins := k + 1
					topDists[ins] = d
					topLabels[ins] = label
				}
				vPtr += 32
			}
		}
		ptr += numVectors * 32
	}
}
