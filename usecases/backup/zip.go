package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// type compressor struct{}
type zip struct {
	sourcePath string
	gzw        *gzip.Writer
	w          *tar.Writer
}

func NewZip(sourcePath string, w io.Writer) zip {
	gzw := gzip.NewWriter(w)
	return zip{
		sourcePath: sourcePath,
		gzw:        gzw,
		w:          tar.NewWriter(gzw),
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

func (z *zip) WriteRegular(relPath string) (written int64, err error) {
	// open file for read
	absPath := filepath.Join(z.sourcePath, relPath)
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

func (z *zip) WriteRegulars(relPaths []string) (written int64, err error) {
	for _, relPath := range relPaths {
		if filepath.Base(relPath) == ".DS_Store" {
			continue
		}
		n, err := z.WriteRegular(relPath)
		if err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}

type unzip struct {
	destPath string
	gzr      *gzip.Reader
	r        *tar.Reader
}

func NewUnzip(dst string, r io.Reader) (unzip, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return unzip{}, fmt.Errorf("gzip.NewReader: %w", err)
	}
	return unzip{
		destPath: dst,
		gzr:      gz,
		r:        tar.NewReader(gz),
	}, nil
}

func (u *unzip) Close() (err error) {
	if u.gzr != nil {
		err = u.gzr.Close()
		u.gzr = nil
		return
	}
	return nil
}

func (u *unzip) ReadRegulars() (written int64, err error) {
	parentPath := ""
	for {
		header, err := u.r.Next()
		if err != nil {
			if err == io.EOF { // end of the loop
				return written, nil
			}
			return written, fmt.Errorf("fetch next: %w", err)
		}
		if header == nil {
			continue
		}
		// target file
		target := filepath.Join(u.destPath, header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return written, fmt.Errorf("crateDir %s: %w", target, err)
			}
		case tar.TypeReg:
			if pp := filepath.Dir(target); pp != parentPath {
				parentPath = pp

				fmt.Println(parentPath)
				fmt.Println("----------------------------------------------------------------")

				if err := os.MkdirAll(parentPath, 0o755); err != nil {
					return written, fmt.Errorf("crateDir %s: %w", target, err)
				}
			}
			fmt.Println("+", target)
			n, err := copyFile(target, os.FileMode(header.Mode), u.r)
			if err != nil {
				return written, fmt.Errorf("copy file %s: %w", target, err)
			}
			written += n
		}
	}
}

func copyFile(target string, perm fs.FileMode, r io.Reader) (written int64, err error) {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, perm)
	if err != nil {
		return written, fmt.Errorf("create: %w", err)
	}
	defer f.Close()
	written, err = io.Copy(f, r)
	if err != nil {
		return written, fmt.Errorf("copy: %w", err)
	}
	return written, nil
}
