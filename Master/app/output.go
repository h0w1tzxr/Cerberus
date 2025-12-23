package master

import (
	"fmt"
	"os"
	"strings"
)

type batchOutput struct {
	path    string
	results []string
	done    int
}

type outputWrite struct {
	path  string
	lines []string
}

func newBatchOutput(path string, total int) *batchOutput {
	if total < 0 {
		total = 0
	}
	return &batchOutput{
		path:    path,
		results: make([]string, total),
	}
}

func (b *batchOutput) setResult(index int, value string) {
	if b == nil || index < 0 || index >= len(b.results) {
		return
	}
	if b.results[index] == "" {
		b.done++
	}
	b.results[index] = value
}

func (s *masterState) newBatchIDLocked() string {
	s.nextBatchSeq++
	return fmt.Sprintf("batch-%d", s.nextBatchSeq)
}

func (s *masterState) ensureBatchOutputLocked(batchID, path string, total int) *batchOutput {
	if s.batchOutputs == nil {
		s.batchOutputs = make(map[string]*batchOutput)
	}
	output, ok := s.batchOutputs[batchID]
	if ok {
		return output
	}
	output = newBatchOutput(path, total)
	s.batchOutputs[batchID] = output
	return output
}

func (s *masterState) recordTaskOutputLocked(task *Task) *outputWrite {
	if task == nil || task.OutputPath == "" {
		return nil
	}
	if task.BatchID == "" {
		return &outputWrite{
			path:  task.OutputPath,
			lines: []string{task.FoundPassword},
		}
	}
	output := s.ensureBatchOutputLocked(task.BatchID, task.OutputPath, task.BatchTotal)
	output.setResult(task.BatchIndex, task.FoundPassword)
	lines := append([]string(nil), output.results...)
	return &outputWrite{
		path:  output.path,
		lines: lines,
	}
}

func (o *outputWrite) write() error {
	if o == nil || o.path == "" {
		return nil
	}
	return writeOutputFile(o.path, o.lines)
}

func writeOutputFile(path string, lines []string) error {
	if path == "" {
		return nil
	}
	var builder strings.Builder
	for i, value := range lines {
		if i > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(value)
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}
