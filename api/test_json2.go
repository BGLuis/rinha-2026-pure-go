package main

import (
	"fmt"
	"github.com/buger/jsonparser"
)

func main() {
	data := []byte(`{"known": ["MERC-016"]}`)
	jsonparser.ArrayEach(data, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
		fmt.Printf("Value: %s (len: %d), Type: %v\n", string(value), len(value), dataType)
	}, "known")
}
