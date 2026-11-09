package backend

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"
)

// defaultSheetName matches the sheet name excelize gives a new workbook; a
// blank or fully-sanitized-away SheetName keeps it.
const defaultSheetName = "Sheet1"

// BuildResultXLSXRequest describes one result-set export: header column names
// plus data rows, where a nil cell renders as an empty cell (matching the CSV
// export's null → empty-cell semantics). Only the rows handed over are
// written — under server-side pagination that means the currently fetched
// page, exactly like the CSV/JSONL exports.
type BuildResultXLSXRequest struct {
	Columns   []string    `json:"columns"`
	Rows      [][]*string `json:"rows"`
	SheetName string      `json:"sheet_name"`
}

// BuildResultXLSX renders a query result set as a single-sheet XLSX workbook:
// row 1 carries the bolded header, the data rows follow. There is no row cap —
// in-memory excelize comfortably handles large result sets, and oversized
// exports are the caller's responsibility (it only sends what it fetched).
// The binding returns []byte; over the Wails JSON bridge that travels as a
// base64 string, which the frontend decodes before saving (see
// frontend/src/utils/export.ts). No repository precedent existed for []byte
// bindings, so the base64 contract is documented on both sides.
func (a *App) BuildResultXLSX(req BuildResultXLSXRequest) ([]byte, error) {
	return buildResultXLSX(req)
}

// buildResultXLSX is the testable core of BuildResultXLSX (no Wails state).
func buildResultXLSX(req BuildResultXLSXRequest) ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	sheet := sanitizeSheetName(req.SheetName)
	if err := f.SetSheetName(defaultSheetName, sheet); err != nil {
		return nil, fmt.Errorf("设置工作表名: %w", err)
	}

	// Header row: bold style applied to the whole span at once.
	for i, col := range req.Columns {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			return nil, fmt.Errorf("表头坐标: %w", err)
		}
		if err := f.SetCellValue(sheet, cell, col); err != nil {
			return nil, fmt.Errorf("写入表头: %w", err)
		}
	}
	if len(req.Columns) > 0 {
		first, _ := excelize.CoordinatesToCellName(1, 1)
		last, _ := excelize.CoordinatesToCellName(len(req.Columns), 1)
		bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
		if err != nil {
			return nil, fmt.Errorf("创建表头样式: %w", err)
		}
		if err := f.SetCellStyle(sheet, first, last, bold); err != nil {
			return nil, fmt.Errorf("应用表头样式: %w", err)
		}
	}

	// Data rows start at row 2; nil cells are skipped so they stay empty.
	for r, row := range req.Rows {
		for c, v := range row {
			if v == nil {
				continue
			}
			cell, err := excelize.CoordinatesToCellName(c+1, r+2)
			if err != nil {
				return nil, fmt.Errorf("单元格坐标: %w", err)
			}
			if err := f.SetCellValue(sheet, cell, *v); err != nil {
				return nil, fmt.Errorf("写入单元格: %w", err)
			}
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, fmt.Errorf("生成 XLSX: %w", err)
	}
	return buf.Bytes(), nil
}

// sanitizeSheetName maps an arbitrary label (e.g. a table name or query title)
// onto excelize's sheet-name rules: forbidden characters []:*?/\ become
// underscores, the name is capped at 31 runes, and empty results fall back to
// the default sheet name.
func sanitizeSheetName(name string) string {
	replacer := strings.NewReplacer(":", "_", "\\", "_", "/", "_", "?", "_", "*", "_", "[", "_", "]", "_")
	sheet := strings.TrimSpace(replacer.Replace(name))
	runes := []rune(sheet)
	if len(runes) > 31 {
		runes = runes[:31]
		sheet = string(runes)
	}
	sheet = strings.TrimSpace(sheet)
	if sheet == "" {
		return defaultSheetName
	}
	return sheet
}

// SaveBinaryFileRequest carries one binary export payload to store on disk.
// The base64 content decodes to the exact bytes written — SaveTextFile cannot
// carry binary because a JS→Go JSON string is UTF-8 encoded on the wire, which
// corrupts bytes ≥ 0x80.
type SaveBinaryFileRequest struct {
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64"`
	Mime          string `json:"mime,omitempty"`
}

// SaveBinaryFile mirrors SaveTextFile (native save dialog, cancelled dialog =
// empty path and no error) but writes decoded binary content, so XLSX and
// other binary exports go through the same dialog-driven path that bypasses
// the download-less WKWebView.
func (a *App) SaveBinaryFile(req SaveBinaryFileRequest) (string, error) {
	dialog := a.dialog
	if dialog == nil {
		dialog = defaultSaveDialog
	}
	path, err := dialog(a.ctx, SaveDialogOptions{DefaultFilename: req.Filename})
	if err != nil {
		return "", fmt.Errorf("保存对话框: %w", err)
	}
	if path == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(req.ContentBase64)
	if err != nil {
		return "", fmt.Errorf("解码导出内容: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("写入 %s: %w", path, err)
	}
	return path, nil
}
