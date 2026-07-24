package report

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dnsbench/internal/model"
)

func WriteExports(dir, prefix string, res *model.RunResult, formats []string, includeRaw bool, mode model.RankMode) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ts := res.Info.StartedAt.Format("20060102-150405")
	var paths []string
	for _, format := range formats {
		ext := strings.ToLower(strings.TrimSpace(format))
		var exporter func(io.Writer) error
		switch ext {
		case "json":
			exporter = func(w io.Writer) error { return ExportJSON(w, res, includeRaw) }
		case "csv":
			exporter = func(w io.Writer) error { return ExportCSV(w, res) }
		case "txt":
			exporter = func(w io.Writer) error { return ExportText(w, res) }
		case "html":
			exporter = func(w io.Writer) error { return ExportHTML(w, res, mode) }
		default:
			return paths, fmt.Errorf("unsupported export format %q (accepted: json, csv, txt, html)", format)
		}
		path := filepath.Join(dir, fmt.Sprintf("%s-%s.%s", prefix, ts, ext))
		if err := writeExportFile(path, exporter); err != nil {
			return paths, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func writeExportFile(path string, exporter func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := exporter(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
