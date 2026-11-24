//go:build windows

package transport

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestLocalJobMetaConcurrentWriteRead cobre a corrida entre o exit watcher / stop (saveMeta)
// e o status / wait (loadMeta) sobre o mesmo meta.json. Antes, o rename falhava com
// "Acesso negado" no Windows enquanto um leitor tinha o arquivo aberto.
func TestLocalJobMetaConcurrentWriteRead(t *testing.T) {
	dir := t.TempDir()
	const id = "20261124_065905_053984"
	m := LocalJobMeta{ID: id, State: jobStateRunning}
	if err := saveMeta(dir, m); err != nil {
		t.Fatal(err)
	}

	var writeErrs, readErrs atomic.Int64
	var firstErr sync.Once
	var firstMsg string
	note := func(err error) {
		firstErr.Do(func() { firstMsg = err.Error() })
	}

	var writers sync.WaitGroup
	for w := 0; w < 2; w++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; i < 300; i++ {
				mm := m
				if i%2 == 0 {
					mm.State = jobStateStopped
				}
				if err := saveMeta(dir, mm); err != nil {
					writeErrs.Add(1)
					note(err)
				}
			}
		}()
	}

	done := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 2; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				if _, err := loadMeta(dir, id); err != nil {
					readErrs.Add(1)
					note(err)
				}
			}
		}()
	}

	writers.Wait()
	close(done)
	readers.Wait()

	if n := writeErrs.Load(); n > 0 {
		t.Errorf("saveMeta falhou %d vez(es) sob concorrência; primeiro erro: %s", n, firstMsg)
	}
	if n := readErrs.Load(); n > 0 {
		t.Errorf("loadMeta falhou %d vez(es) sob concorrência; primeiro erro: %s", n, firstMsg)
	}
}
