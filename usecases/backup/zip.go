package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type compressor struct{}

type zip struct {
	dataPath string
	w        *tar.Writer
	gzw      *gzip.Writer
}

func NewZip(sourcePath string, w io.Writer) zip {
	gzw := gzip.NewWriter(w)
	return zip{
		dataPath: sourcePath,
		gzw:      gzw,
		w:        tar.NewWriter(gzw),
	}
}

func (z *zip) Close() (err error) {
	if z.w != nil {
		err = z.w.Close()
		z.w = nil
		return
	}
	if z.gzw != nil {
		err = z.gzw.Close()
		z.gzw = nil
		return
	}
	return nil
}

func (z *zip) AddRegular(relPath string) (written int64, err error) {
	// open file for read
	absPath := filepath.Join(z.dataPath, relPath)
	info, err := os.Stat(absPath)
	if err != nil {
		return written, fmt.Errorf("stat: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, nil // ignore directories
	}
	f, err := os.Open(absPath)
	if err != nil {
		return written, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	// write info header
	header, err := tar.FileInfoHeader(info, info.Name())
	if err != nil {
		return written, fmt.Errorf("file header: %w", err)
	}
	header.Name = relPath
	if err := z.w.WriteHeader(header); err != nil {
		return written, fmt.Errorf("write header %s: %w", relPath, err)
	}
	// write bytes
	written, err = io.Copy(z.w, f)
	if err != nil {
		return written, fmt.Errorf("copy: %s %w", relPath, err)
	}
	return
}

func (z *zip) AddRegulars(filepaths []string) (written int64, err error) {
	for _, relPath := range filepaths {
		if filepath.Base(relPath) == ".DS_Store" {
			continue
		}
		n, err := z.AddRegular(relPath)
		if err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}
