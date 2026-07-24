package report

import (
	"fmt"
	"strings"

	"dnsbench/internal/model"
)

type svgBar struct {
	cat    model.Category
	median float64
}

type svgRow struct {
	label string
	bars  []svgBar
}

type svgTheme struct {
	surface   string
	ink       string
	secondary string
	muted     string
	cat       map[model.Category]string
}

var svgLight = svgTheme{
	surface:   "#fcfcfb",
	ink:       "#0b0b0b",
	secondary: "#52514e",
	muted:     "#898781",
	cat: map[model.Category]string{
		model.CatCached:   "#2a78d6",
		model.CatUncached: "#eb6834",
		model.CatTLD:      "#1baf7a",
	},
}

var svgDark = svgTheme{
	surface:   "#1a1a19",
	ink:       "#ffffff",
	secondary: "#c3c2b7",
	muted:     "#898781",
	cat: map[model.Category]string{
		model.CatCached:   "#3987e5",
		model.CatUncached: "#d95926",
		model.CatTLD:      "#199e70",
	},
}

var xmlReplacer = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

func xmlEscape(s string) string {
	return xmlReplacer.Replace(s)
}

func (t svgTheme) color(cat model.Category) string {
	if c, ok := t.cat[cat]; ok {
		return c
	}
	return t.muted
}

func writeSVG(b *strings.Builder, res *model.RunResult, mode model.RankMode, theme svgTheme) {
	rows := svgRows(res, mode)
	maxMedian := 0.0
	for _, r := range rows {
		for _, bar := range r.bars {
			if bar.median > maxMedian {
				maxMedian = bar.median
			}
		}
	}
	if maxMedian <= 0 {
		maxMedian = 1
	}
	const (
		width    = 860.0
		labelW   = 240.0
		valueW   = 80.0
		barH     = 14.0
		barGap   = 6.0
		rowHead  = 18.0
		rowGap   = 16.0
		topH     = 64.0
		bottomH  = 16.0
		emptyH   = 40.0
		plotPad  = 16.0
		fontMain = 12
	)
	plotW := width - labelW - valueW - plotPad
	height := topH + bottomH
	for _, r := range rows {
		height += rowHead + float64(len(r.bars))*(barH+barGap) + rowGap
	}
	if len(rows) == 0 {
		height += emptyH
	}

	fmt.Fprintf(b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" role="img">`+"\n",
		width, height, width, height)
	fmt.Fprintf(b, `<rect x="0" y="0" width="%.0f" height="%.0f" fill="%s"/>`+"\n", width, height, theme.surface)
	fmt.Fprintf(b, `<g font-family="system-ui, sans-serif" font-size="%d" fill="%s">`+"\n", fontMain, theme.ink)
	fmt.Fprintf(b, `<text x="16" y="24" font-size="15" font-weight="600">%s</text>`+"\n",
		xmlEscape(fmt.Sprintf("Median latency by category — %s ranking", mode.Label())))

	x := 16.0
	for _, cat := range legendCategories(rows) {
		fmt.Fprintf(b, `<rect x="%.1f" y="37" width="10" height="10" rx="3" fill="%s"/>`+"\n", x, theme.color(cat))
		label := cat.Label()
		fmt.Fprintf(b, `<text x="%.1f" y="46" font-size="11" fill="%s">%s</text>`+"\n", x+15, theme.secondary, xmlEscape(label))
		x += 15 + float64(len(label))*7 + 24
	}

	y := topH
	for _, r := range rows {
		fmt.Fprintf(b, `<text x="16" y="%.1f" font-weight="600" font-size="12">%s</text>`+"\n", y+13, xmlEscape(r.label))
		y += rowHead
		for _, bar := range r.bars {
			bw := bar.median / maxMedian * plotW
			if bw < 1 {
				bw = 1
			}
			fmt.Fprintf(b, `<path d="%s" fill="%s"><title>%s</title></path>`+"\n",
				roundedEndBarPath(labelW, y, bw, barH), theme.color(bar.cat),
				xmlEscape(fmt.Sprintf("%s — %s median %.1f ms", r.label, bar.cat.Label(), bar.median)))
			fmt.Fprintf(b, `<text x="%.1f" y="%.1f" font-size="11" fill="%s">%s</text>`+"\n",
				labelW+bw+6, y+barH-3, theme.secondary, xmlEscape(fmt.Sprintf("%.1f ms", bar.median)))
			y += barH + barGap
		}
		y += rowGap
	}
	if len(rows) == 0 {
		fmt.Fprintf(b, `<text x="16" y="%.1f" fill="%s">No active servers to chart.</text>`+"\n", topH+20, theme.secondary)
	}
	b.WriteString("</g>\n</svg>\n")
}

func roundedEndBarPath(x, y, w, h float64) string {
	const r = 4.0
	if w <= r {
		return fmt.Sprintf("M%.1f,%.1f h%.1f v%.1f h-%.1f z", x, y, w, h, w)
	}
	return fmt.Sprintf("M%.1f,%.1f h%.1f a%.0f,%.0f 0 0 1 %.0f,%.0f v%.1f a%.0f,%.0f 0 0 1 -%.0f,%.0f h-%.1f z",
		x, y, w-r, r, r, r, r, h-2*r, r, r, r, r, w-r)
}

const svgMaxRows = 15

func svgRows(res *model.RunResult, mode model.RankMode) []svgRow {
	var order []string
	ranked := rankedScores(res, mode)
	if len(ranked) > 0 {
		for _, sc := range ranked {
			order = append(order, sc.ServerID)
		}
	} else {
		for i := range res.Servers {
			order = append(order, res.Servers[i].ID)
		}
	}
	var rows []svgRow
	for _, id := range order {
		if stateOf(res, id) != model.StateActive {
			continue
		}
		st := res.Stats[id]
		if st == nil {
			continue
		}
		var bars []svgBar
		for _, cat := range model.AllCategories() {
			d := st.PerCategory[cat]
			if d == nil || d.Valid == 0 {
				continue
			}
			bars = append(bars, svgBar{cat: cat, median: d.MedianMs})
		}
		if len(bars) == 0 {
			continue
		}
		rows = append(rows, svgRow{
			label: fmt.Sprintf("%s (loss %.1f%%)", displayNameByID(res, id), aggregateLoss(st)),
			bars:  bars,
		})
		if len(rows) >= svgMaxRows {
			break
		}
	}
	return rows
}

func legendCategories(rows []svgRow) []model.Category {
	present := map[model.Category]bool{}
	for _, r := range rows {
		for _, bar := range r.bars {
			present[bar.cat] = true
		}
	}
	var cats []model.Category
	for _, cat := range model.AllCategories() {
		if present[cat] {
			cats = append(cats, cat)
		}
	}
	return cats
}
