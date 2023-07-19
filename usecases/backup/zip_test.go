package backup

import (
	"path/filepath"
	"testing"
)

func TestXxx(t *testing.T) {
	// f, err := os.Open("../backup")
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// info, err := f.Stat()
	// t.Error(info.IsDir(), info.Size(), info.ModTime(), err)
	relPath := "b/c"
	parentPath := "/"
	absPath := filepath.Join(parentPath, relPath)
	t.Error(absPath)
}
