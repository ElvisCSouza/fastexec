package telemetry

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	setupAuditSchema(db)
	return db
}

func TestLedgerPerGoroutine(t *testing.T) {
	var wg sync.WaitGroup
	results := make([][]Saving, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			BeginCall()
			NoteSaving(CatClean, 1000*(i+1), 100)
			NoteSaving(CatCut, 50, 100) // sem economia: não anota
			NoteAvoided(CatRetry, 40, 0)
			results[i] = EndCall()
		}(i)
	}
	wg.Wait()
	for i, got := range results {
		if len(got) != 2 {
			t.Fatalf("goroutine %d: %d itens, esperado 2: %+v", i, len(got), got)
		}
		if want := 1000*(i+1) - 100; got[0].Bytes != want || got[0].Cat != CatClean || !got[0].Exact {
			t.Errorf("goroutine %d: %+v, esperado %d bytes de limpeza", i, got[0], want)
		}
		if got[1].Trips != 1 || got[1].Ms != AvoidedRoundTripMs || got[1].Exact {
			t.Errorf("goroutine %d: retentativa %+v", i, got[1])
		}
	}
	if EndCall() != nil {
		t.Error("EndCall sem BeginCall deve devolver nil")
	}
}

func TestSavingsReport(t *testing.T) {
	db := openTestDB(t)
	e := AuditEntry{Tool: "exec", Host: "local", Success: true, BytesOut: 3500, EstTokensOut: 1000, DurationMs: 200, CommandSummary: "go test ./...",
		Savings: []Saving{{Cat: CatClean, Bytes: 7000, Exact: true}, {Cat: CatCut, Bytes: 3500, Exact: true}}}
	if err := writeEntry(db, e); err != nil {
		t.Fatal(err)
	}
	e2 := AuditEntry{Tool: "exec", Host: "local", Success: true, BytesOut: 350, EstTokensOut: 100, DurationMs: 50, CommandSummary: "cat a.txt",
		Savings: []Saving{{Cat: CatRetry, Bytes: 700, Trips: 1, Ms: AvoidedRoundTripMs}}}
	if err := writeEntry(db, e2); err != nil {
		t.Fatal(err)
	}
	if err := writeSavings(db, 0, []Saving{{Cat: CatSchema, Bytes: 3500, Exact: true}}); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"today", "week", "all"} {
		r, err := buildSavingsReport(db, p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if r.Calls != 2 || r.SentTokens != 1100 {
			t.Errorf("%s: calls=%d sent=%d", p, r.Calls, r.SentTokens)
		}
		// 7000+3500+700+3500 bytes = 14700 / 3.5 = 4200 tokens
		if r.SavedTokens != 4200 || r.WithoutTokens != 5300 {
			t.Errorf("%s: saved=%d without=%d", p, r.SavedTokens, r.WithoutTokens)
		}
		if r.TripsAvoided != 1 || r.FailuresAvoided != 1 || r.SavedMs != AvoidedRoundTripMs {
			t.Errorf("%s: trips=%d failures=%d ms=%d", p, r.TripsAvoided, r.FailuresAvoided, r.SavedMs)
		}
		if len(r.Cats) != 4 || r.Cats[0].Cat != CatClean {
			t.Errorf("%s: categorias %+v", p, r.Cats)
		}
		var sum int64
		for _, v := range r.Series {
			sum += v
		}
		if sum != 4200 {
			t.Errorf("%s: soma da série %d, esperado 4200 (%v)", p, sum, r.Series)
		}
		if len(r.Recent) != 3 {
			t.Errorf("%s: %d eventos recentes, esperado 3", p, len(r.Recent))
		}
	}
}
