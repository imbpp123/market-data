// Command contract-sizes exports fixed messages for cross-language comparison.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/imbpp123/market-data/api/go/internal/fixture"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Usage: contract-sizes OUTPUT_DIRECTORY")
	}
	if err := os.MkdirAll(os.Args[1], 0755); err != nil {
		log.Fatal(err)
	}
	messages := map[string]proto.Message{}
	for _, count := range []int{1, 20000} {
		messages[fmt.Sprintf("instruments-%d", count)] = fixture.Instruments(count)
		messages[fmt.Sprintf("tickers-%d", count)] = fixture.Tickers(count)
		messages[fmt.Sprintf("market-stats-%d", count)] = fixture.Stats(count)
	}
	for _, count := range []int{1, 100, 1000} {
		messages[fmt.Sprintf("klines-%d", count)] = fixture.Klines(count)
	}
	sizes := map[string]int{}
	for name, message := range messages {
		data, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
		if err != nil {
			log.Fatal(err)
		}
		sizes[name] = len(data)
		if len(data) > fixture.ReceiveLimit {
			log.Fatalf("%s exceeds response limit", name)
		}
		if err := os.WriteFile(filepath.Join(os.Args[1], name+".pb"), data, 0644); err != nil {
			log.Fatal(err)
		}
		normalized, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(os.Args[1], name+".json"), normalized, 0644); err != nil {
			log.Fatal(err)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(sizes); err != nil {
		log.Fatal(err)
	}
}
