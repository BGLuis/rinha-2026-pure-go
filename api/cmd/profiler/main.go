package main

import (
	"log"
	"math/rand"
	"os"
	"runtime/pprof"

	"rinha-api/engine"
)

func main() {
	res := engine.InitEngine("dataset.bin")
	if res < 0 {
		log.Fatalf("init failed: %d", res)
	}

	f, err := os.Create("cpu.pprof")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	rng := rand.New(rand.NewSource(42))
	var q [14]float32
	var scratch byte

	for i := 0; i < 50000; i++ {
		for j := 0; j < 14; j++ {
			q[j] = rng.Float32()
		}
		engine.SearchVectorFast(&q[0], &scratch)
	}

	log.Println("PGO profile collected: cpu.pprof")
}
