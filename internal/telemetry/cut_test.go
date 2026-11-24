package telemetry

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func openCutDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "cut.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	setupAuditSchema(db)
	return db
}

func TestNoteCutRecordsDetailOnlyWhenSmaller(t *testing.T) {
	BeginCall()
	NoteCut(100, 100, "same", CutDetail{})
	NoteCut(1000, 400, "cut", CutDetail{OrigLines: 9, OrigBytes: 1000, RetLines: 3, RetBytes: 400, Reason: "teto"})
	items := EndCall()
	if len(items) != 1 {
		t.Fatalf("esperava 1 registro, veio %d", len(items))
	}
	s := items[0]
	if s.Cat != CatCut || s.Bytes != 600 || s.Before != 1000 || s.After != 400 || !s.Exact {
		t.Fatalf("registro de corte errado: %+v", s)
	}
	if s.Cut == nil || s.Cut.OmitBytes != 0 || s.Cut.OrigBytes != 1000 || s.Cut.RetBytes != 400 {
		t.Fatalf("detalhe do corte errado: %+v", s.Cut)
	}
}

func TestCutDetailPersistsAndNonCutRowsStayNull(t *testing.T) {
	db := openCutDB(t)
	d := CutDetail{OrigLines: 2000, OrigBytes: 50000, RetLines: 300, RetBytes: 9000,
		OmitLines: 1700, OmitBytes: 41000, Reason: "teto", Filter: "head+tail",
		Cursor: "read_file(path, ranges='301-381')", Spill: "C:/x/20261124-101010-abcdef12.txt"}
	if err := writeSavings(db, 1, []Saving{
		{Cat: CatCut, Bytes: 41000, Exact: true, Before: 50000, After: 9000, Note: "n", Cut: &d},
		{Cat: CatClean, Bytes: 10, Exact: true, Before: 20, After: 10},
	}); err != nil {
		t.Fatal(err)
	}
	var omit int
	var filter, cursor string
	if err := db.QueryRow(`SELECT cut_omit_bytes, cut_filter, cut_cursor FROM saving WHERE cat = ?`, CatCut).
		Scan(&omit, &filter, &cursor); err != nil {
		t.Fatal(err)
	}
	if omit != 41000 || filter != "head+tail" || cursor != d.Cursor {
		t.Fatalf("colunas de corte erradas: omit=%d filter=%q cursor=%q", omit, filter, cursor)
	}
	var nullCut int
	if err := db.QueryRow(`SELECT COUNT(*) FROM saving WHERE cat = ? AND cut_omit_bytes IS NULL AND cut_recovered IS NULL`,
		CatClean).Scan(&nullCut); err != nil || nullCut != 1 {
		t.Fatalf("linha que não é corte deveria ter colunas de corte NULL: %d %v", nullCut, err)
	}
}

func TestMigrationKeepsOldRowsUnknown(t *testing.T) {
	db := openCutDB(t)
	if err := writeSavings(db, 1, []Saving{{Cat: CatCut, Bytes: 500, Exact: true, Before: 900, After: 400, Note: "old"}}); err != nil {
		t.Fatal(err)
	}
	// simulate a database written before the cut columns existed, then run the migration again
	for col := range cutColumns {
		if _, err := db.Exec(`ALTER TABLE saving DROP COLUMN ` + col); err != nil {
			t.Fatalf("drop %s: %v", col, err)
		}
	}
	setupAuditSchema(db)
	var unknown int
	if err := db.QueryRow(`SELECT COUNT(*) FROM saving WHERE cat = ? AND cut_omit_bytes IS NULL`, CatCut).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if unknown != 1 {
		t.Fatalf("linha antiga de corte deveria ficar desconhecida (NULL), não %d", unknown)
	}
	s, err := queryCutSummary(db, "1=1")
	if err != nil {
		t.Fatal(err)
	}
	if s.cuts != 1 || s.unknown != 1 || s.neverAccessedOmit != 0 {
		t.Fatalf("resumo com linha antiga errado: %+v", s)
	}
}

func TestMarkCutRecoveredAndRecoveryBlock(t *testing.T) {
	db := openCutDB(t)
	spillA := filepath.Clean("/spill/20261124-101010-aaaaaaaa.txt")
	spillB := filepath.Clean("/spill/20261124-111111-bbbbbbbb.txt")
	mk := func(spill string, omit int) CutDetail {
		return CutDetail{OrigBytes: omit + 100, RetBytes: 100, OmitBytes: omit, Reason: "teto", Filter: "head+tail", Spill: spill}
	}
	if err := writeSavings(db, 1, []Saving{
		{Cat: CatCut, Bytes: 1000, Exact: true, Before: 1100, After: 100, Cut: ptr(mk(spillA, 1000))},
		{Cat: CatCut, Bytes: 3000, Exact: true, Before: 3100, After: 100, Cut: ptr(mk(spillB, 3000))},
	}); err != nil {
		t.Fatal(err)
	}

	prev := dbInstance
	dbInstance = db
	defer func() { dbInstance = prev }()
	MarkCutRecovered(strings.ToUpper(spillA)) // case-insensitive match on the spill path
	MarkCutRecovered("")                      // no-op

	s, err := queryCutSummary(db, "1=1")
	if err != nil {
		t.Fatal(err)
	}
	// Only access is measured: spillA was opened once, spillB never. The 1000 omitted bytes of spillA
	// are not counted as recovered, and the block must not say they were.
	if s.cuts != 2 || s.accessed != 1 || s.neverAccessedOmit != 3000 {
		t.Fatalf("resumo de acesso errado: %+v", s)
	}
	out := renderCutRecovery(s)
	for _, want := range []string{
		"Cut recovery (2 cuts; 2 with detail, 0 older rows unknown)",
		"spill opened with read_file at least once: 1 cuts",
		"omitted bytes of cuts never opened: 3000",
		"an open is not full recovery",
		"masked secrets are not counted",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("bloco de recuperação sem %q\n%s", want, out)
		}
	}
	for _, bad := range []string{"recovered", "read back", "1000 omitted"} {
		if strings.Contains(out, bad) {
			t.Fatalf("bloco não pode afirmar recuperação total (%q)\n%s", bad, out)
		}
	}
}

func TestRecoveryDoesNotChangeSavings(t *testing.T) {
	db := openCutDB(t)
	spill := filepath.Clean("/spill/20261124-121212-cccccccc.txt")
	d := CutDetail{OrigBytes: 5000, RetBytes: 500, OmitBytes: 4500, Reason: "teto", Filter: "head+tail", Spill: spill}
	if err := writeSavings(db, 1, []Saving{{Cat: CatCut, Bytes: 4500, Exact: true, Before: 5000, After: 500, Cut: &d}}); err != nil {
		t.Fatal(err)
	}
	prev := dbInstance
	dbInstance = db
	defer func() { dbInstance = prev }()
	MarkCutRecovered(spill)

	var bytes int
	if err := db.QueryRow(`SELECT SUM(bytes) FROM saving WHERE cat = ?`, CatCut).Scan(&bytes); err != nil {
		t.Fatal(err)
	}
	if bytes != 4500 {
		t.Fatalf("recuperar o corte não pode alterar a economia gravada: %d", bytes)
	}
}

func ptr[T any](v T) *T { return &v }
