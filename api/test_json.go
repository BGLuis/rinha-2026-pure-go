package main

import (
	"fmt"
	"github.com/buger/jsonparser"
)

func main() {
	data := []byte(`{"mcc": "5411"}`)
	jsonparser.EachKey(data, func(idx int, value []byte, vt jsonparser.ValueType, err error) {
		fmt.Printf("Value: %s (len: %d), Type: %v\n", string(value), len(value), vt)
	}, []string{"mcc"})
}
