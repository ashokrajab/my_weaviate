package backup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestZip(t *testing.T) {
	var (
		pathNode  = "./test_data/node1"
		pathFiles = pathNode + "/files.json"
		files     = make([]string, 64)
		pathDest  = "./test_data/node-unzipped"
	)
	data, err := os.ReadFile(pathFiles)
	if err != nil {
		t.Fatalf("read relative source file paths: %v", err)
	}
	if err := json.Unmarshal(data, &files); err != nil {
		t.Fatalf("unmarshal relative source file paths: %v", err)
	}

	compressBuf := bytes.NewBuffer(make([]byte, 0, 1000_000))
	z := NewZip(pathNode, compressBuf)
	readZ, err := z.WriteRegulars(files)
	z.Close()
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	writtenZ := int64(compressBuf.Len())
	if writtenZ != readZ {
		fmt.Printf("%v - %v compression factor=%v\n", writtenZ, readZ, float32(readZ)/float32(writtenZ))
	}

	uz, err := NewUnzip(pathDest, compressBuf)
	if err != nil {
		t.Fatalf("NewUnzip: %v", err)
	}
	writtenUZ, err := uz.ReadRegulars()
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	uz.Close()

	_, err = os.Stat(pathDest)
	if err != nil {
		t.Fatalf("cannot find decompressed folder: %v", err)
	}

	if readZ != writtenUZ {
		t.Errorf("uncompressed size want=%v got=%v", writtenUZ, readZ)
	}
}
