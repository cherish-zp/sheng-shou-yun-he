package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"sheng-shou-yun-he/backend/internal/model"
	"sheng-shou-yun-he/backend/internal/service"
)

// strPtr is a tiny helper for building result rows.
func strPtr(s string) *string { return &s }

// TestBuildResultXLSXRoundTrip builds a workbook with Chinese headers, a nil
// cell and Chinese data, then re-opens the bytes with excelize to assert the
// sheet name, bolded header and every cell value. It also checks the ZIP
// magic (PK) since XLSX is a ZIP container.
func TestBuildResultXLSXRoundTrip(t *testing.T) {
	req := BuildResultXLSXRequest{
		Columns:   []string{"名称", "数量", "备注"},
		SheetName: "查询结果",
		Rows: [][]*string{
			{strPtr("a"), nil, strPtr("中文内容")},
			{strPtr("1"), strPtr("2"), nil},
		},
	}

	data, err := buildResultXLSX(req)
	if err != nil {
		t.Fatalf("buildResultXLSX: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty XLSX bytes")
	}
	if !bytes.HasPrefix(data, []byte("PK")) {
		t.Fatalf("expected ZIP magic header PK, got % x", data[:2])
	}

	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	defer func() { _ = f.Close() }()

	sheet := f.GetSheetList()[0]
	if sheet != "查询结果" {
		t.Fatalf("unexpected sheet name %q", sheet)
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	// GetRows renders empty cells as "" and drops trailing empties, so the
	// expected shapes mirror that: header is complete, data rows may be short.
	wantHeader := []string{"名称", "数量", "备注"}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d: %v", len(rows), rows)
	}
	for i, want := range wantHeader {
		if rows[0][i] != want {
			t.Fatalf("header col %d: want %q got %q", i, want, rows[0][i])
		}
	}
	if rows[1][0] != "a" || rows[1][1] != "" || rows[1][2] != "中文内容" {
		t.Fatalf("unexpected row 1: %v", rows[1])
	}
	if rows[2][0] != "1" || rows[2][1] != "2" {
		t.Fatalf("unexpected row 2: %v", rows[2])
	}

	// The header must render bold: read the style back from the first cell.
	styleID, err := f.GetCellStyle(sheet, "A1")
	if err != nil {
		t.Fatalf("GetCellStyle: %v", err)
	}
	style, err := f.GetStyle(styleID)
	if err != nil {
		t.Fatalf("GetStyle: %v", err)
	}
	if style == nil || style.Font == nil || !style.Font.Bold {
		t.Fatalf("header style is not bold: %+v", style)
	}
}

// TestBuildResultXLSXSheetNameSanitized covers labels that violate excelize's
// sheet-name rules: forbidden characters are replaced, >31 runes are capped,
// and an empty name falls back to the default.
func TestBuildResultXLSXSheetNameSanitized(t *testing.T) {
	t.Run("forbidden chars replaced", func(t *testing.T) {
		data, err := buildResultXLSX(BuildResultXLSXRequest{
			Columns:   []string{"c"},
			SheetName: `a/b:c[d]*?`,
			Rows:      [][]*string{{strPtr("x")}},
		})
		if err != nil {
			t.Fatalf("buildResultXLSX: %v", err)
		}
		f, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("open workbook: %v", err)
		}
		defer func() { _ = f.Close() }()
		if got := f.GetSheetList()[0]; got != "a_b_c_d___" {
			t.Fatalf("unexpected sanitized sheet name %q", got)
		}
	})

	t.Run("long name capped at 31 runes", func(t *testing.T) {
		data, err := buildResultXLSX(BuildResultXLSXRequest{
			Columns:   []string{"c"},
			SheetName: strings.Repeat("长", 40),
			Rows:      nil,
		})
		if err != nil {
			t.Fatalf("buildResultXLSX: %v", err)
		}
		f, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("open workbook: %v", err)
		}
		defer func() { _ = f.Close() }()
		got := []rune(f.GetSheetList()[0])
		if len(got) != 31 {
			t.Fatalf("expected sheet name capped to 31 runes, got %d (%q)", len(got), string(got))
		}
	})

	t.Run("empty name falls back to default", func(t *testing.T) {
		data, err := buildResultXLSX(BuildResultXLSXRequest{Columns: []string{"c"}})
		if err != nil {
			t.Fatalf("buildResultXLSX: %v", err)
		}
		f, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("open workbook: %v", err)
		}
		defer func() { _ = f.Close() }()
		if got := f.GetSheetList()[0]; got != defaultSheetName {
			t.Fatalf("expected default sheet name, got %q", got)
		}
	})
}

// TestAppBuildResultXLSXBinding verifies the App binding delegates to the
// builder and returns the same bytes.
func TestAppBuildResultXLSXBinding(t *testing.T) {
	fake := &fakeBatchKafka{}
	app := newTestAppWithFactory(t, service.ClientFactoryFunc(
		func(context.Context, model.KafkaConfig) (service.KafkaDataSource, error) { return fake, nil },
	))

	data, err := app.BuildResultXLSX(BuildResultXLSXRequest{
		Columns:   []string{"id"},
		SheetName: "t",
		Rows:      [][]*string{{strPtr("1")}},
	})
	if err != nil {
		t.Fatalf("BuildResultXLSX: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("PK")) {
		t.Fatalf("expected ZIP magic header PK, got % x", data[:2])
	}
}

// TestAppSaveBinaryFileWritesViaDialog verifies the binary export save path:
// the dialog receives the default filename and the base64 payload decodes to
// the exact bytes on disk (including bytes ≥ 0x80 that SaveTextFile cannot
// carry).
func TestAppSaveBinaryFileWritesViaDialog(t *testing.T) {
	fake := &fakeBatchKafka{}
	app := newTestAppWithFactory(t, service.ClientFactoryFunc(
		func(context.Context, model.KafkaConfig) (service.KafkaDataSource, error) { return fake, nil },
	))
	app.dialog = func(_ context.Context, opts SaveDialogOptions) (string, error) {
		if opts.DefaultFilename != "query-result.xlsx" {
			t.Fatalf("unexpected default filename %q", opts.DefaultFilename)
		}
		return filepath.Join(t.TempDir(), "out.xlsx"), nil
	}

	raw := []byte("PK\x03\x04\xff\x00中文")
	path, err := app.SaveBinaryFile(SaveBinaryFileRequest{
		Filename:      "query-result.xlsx",
		ContentBase64: base64.StdEncoding.EncodeToString(raw),
		Mime:          "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	})
	if err != nil {
		t.Fatalf("SaveBinaryFile: %v", err)
	}
	if path == "" {
		t.Fatal("expected the saved file path back")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if !bytes.Equal(data, raw) {
		t.Fatalf("saved bytes differ: want % x got % x", raw, data)
	}
}

// TestAppSaveBinaryFileCancelledDialogWritesNothing mirrors the SaveTextFile
// cancellation contract: empty path, no error, no file written.
func TestAppSaveBinaryFileCancelledDialogWritesNothing(t *testing.T) {
	fake := &fakeBatchKafka{}
	app := newTestAppWithFactory(t, service.ClientFactoryFunc(
		func(context.Context, model.KafkaConfig) (service.KafkaDataSource, error) { return fake, nil },
	))
	dir := t.TempDir()
	app.dialog = func(context.Context, SaveDialogOptions) (string, error) {
		return "", nil
	}

	path, err := app.SaveBinaryFile(SaveBinaryFileRequest{
		Filename:      "x.xlsx",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("PK")),
	})
	if err != nil {
		t.Fatalf("cancelled dialog must not error: %v", err)
	}
	if path != "" {
		t.Fatalf("cancelled dialog must return an empty path, got %q", path)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("cancelled dialog must not write files, found %v", entries)
	}
}
