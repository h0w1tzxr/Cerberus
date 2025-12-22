package wordlist

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

const (
	DefaultIndexStride  int64 = 1024
	DefaultMaxLineBytes int64 = 1 << 20
	DefaultBufferSize         = 128 * 1024
)

var (
	ErrLineTooLong = errors.New("wordlist line exceeds limit")
	ErrStop        = errors.New("stop iteration")
)

type Index struct {
	Path         string
	Size         int64
	ModTime      time.Time
	LineCount    int64
	Stride       int64
	Offsets      []int64
	MaxLineBytes int64
}

type Cache struct {
	mu           sync.Mutex
	entries      map[string]*Index
	stride       int64
	maxLineBytes int64
}

func NewCache(stride, maxLineBytes int64) *Cache {
	if stride <= 0 {
		stride = DefaultIndexStride
	}
	if maxLineBytes <= 0 {
		maxLineBytes = DefaultMaxLineBytes
	}
	return &Cache{
		entries:      make(map[string]*Index),
		stride:       stride,
		maxLineBytes: maxLineBytes,
	}
}

func (c *Cache) Get(path string) (*Index, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("wordlist %s is a directory", path)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.entries[path]; ok {
		if existing.Size == info.Size() && existing.ModTime.Equal(info.ModTime()) {
			return existing, nil
		}
	}

	index, err := BuildIndex(path, c.stride, c.maxLineBytes)
	if err != nil {
		return nil, err
	}
	c.entries[path] = index
	return index, nil
}

func BuildIndex(path string, stride, maxLineBytes int64) (*Index, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("wordlist %s is a directory", path)
	}
	if stride <= 0 {
		stride = DefaultIndexStride
	}
	if maxLineBytes <= 0 {
		maxLineBytes = DefaultMaxLineBytes
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	offsets := []int64{0}
	var (
		lineCount int64
		lineLen   int64
		offset    int64
	)

	buffer := make([]byte, DefaultBufferSize)
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			for i := 0; i < n; i++ {
				lineLen++
				if buffer[i] == '\n' {
					if lineLen-1 > maxLineBytes {
						return nil, ErrLineTooLong
					}
					lineCount++
					lineLen = 0
					if stride > 0 && lineCount%stride == 0 {
						offsets = append(offsets, offset+int64(i)+1)
					}
				}
			}
			offset += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}

	if lineLen > 0 {
		if lineLen > maxLineBytes {
			return nil, ErrLineTooLong
		}
		lineCount++
	}
	if lineCount == 0 {
		return nil, fmt.Errorf("wordlist %s has no entries", path)
	}

	return &Index{
		Path:         path,
		Size:         info.Size(),
		ModTime:      info.ModTime(),
		LineCount:    lineCount,
		Stride:       stride,
		Offsets:      offsets,
		MaxLineBytes: maxLineBytes,
	}, nil
}

func (idx *Index) OffsetForLine(line int64) (int64, int64) {
	if line <= 0 || idx.Stride <= 0 || len(idx.Offsets) == 0 {
		return 0, 0
	}
	slot := line / idx.Stride
	if slot >= int64(len(idx.Offsets)) {
		slot = int64(len(idx.Offsets) - 1)
	}
	return idx.Offsets[slot], slot * idx.Stride
}

type Reader struct {
	file *os.File
	idx  *Index
	buf  *bufio.Reader
}

func NewReader(idx *Index) (*Reader, error) {
	if idx == nil {
		return nil, errors.New("wordlist index is nil")
	}
	file, err := os.Open(idx.Path)
	if err != nil {
		return nil, err
	}
	return &Reader{
		file: file,
		idx:  idx,
		buf:  bufio.NewReaderSize(file, DefaultBufferSize),
	}, nil
}

func (r *Reader) Close() error {
	if r.file == nil {
		return nil
	}
	return r.file.Close()
}

func (r *Reader) ReadRange(start, end int64, fn func(line string, lineNumber int64) error) error {
	if r == nil || r.idx == nil {
		return errors.New("wordlist reader not initialized")
	}
	if start < 0 || end < start {
		return fmt.Errorf("invalid range %d-%d", start, end)
	}

	offset, baseLine := r.idx.OffsetForLine(start)
	if _, err := r.file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	r.buf.Reset(r.file)

	lineNumber := baseLine
	for lineNumber < start {
		if _, err := r.readLine(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		lineNumber++
	}

	for lineNumber < end {
		line, err := r.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := fn(line, lineNumber); err != nil {
			if errors.Is(err, ErrStop) {
				return nil
			}
			return err
		}
		lineNumber++
	}
	return nil
}

func (r *Reader) readLine() (string, error) {
	var (
		buf []byte
	)
	for {
		linePart, err := r.buf.ReadSlice('\n')
		buf = append(buf, linePart...)
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			if int64(len(buf)) > r.idx.MaxLineBytes {
				return "", ErrLineTooLong
			}
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(buf) == 0 {
				return "", io.EOF
			}
			break
		}
		return "", err
	}

	if len(buf) > 0 && buf[len(buf)-1] == '\n' {
		buf = buf[:len(buf)-1]
	}
	if len(buf) > 0 && buf[len(buf)-1] == '\r' {
		buf = buf[:len(buf)-1]
	}
	if int64(len(buf)) > r.idx.MaxLineBytes {
		return "", ErrLineTooLong
	}
	return string(buf), nil
}
