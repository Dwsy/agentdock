package kit

import (
	"sync"

	"github.com/egoist/mygo/ui"
)

// 本文件内嵌的 SVG 图标取自 Lucide（https://lucide.dev），ISC 许可证：
//
//	Copyright (c) for Lucide, ISC License
//	Permission to use, copy, modify, and/or distribute this software for any
//	purpose with or without fee is hereby granted, provided that the above
//	copyright notice and this permission notice appear in all copies.
//
// 每个图标都是 24x24、fill="none"、stroke="currentColor"、stroke-width="2"、
// stroke-linecap/linejoin="round" 的官方 path，可随文字颜色着色。
// 图标用 sync.Once 惰性 ParseSVG 并缓存，渲染期不再解析。

// iconDef 持有一个图标的 SVG 源码与解析缓存。
type iconDef struct {
	once sync.Once
	src  string
	svg  *ui.SVG
}

// get 惰性解析并缓存 SVG。
func (d *iconDef) get() *ui.SVG {
	d.once.Do(func() {
		s, err := ui.ParseSVG([]byte(d.src))
		if err != nil {
			panic("kit: 解析图标失败: " + err.Error())
		}
		d.svg = s
	})
	return d.svg
}

// iconWrap 包出 Lucide 标准的 24x24 stroke 图标文档。
func iconWrap(body string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" ` +
		`fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
		body + `</svg>`
}

var (
	gaugeIcon        = iconDef{src: iconWrap(`<path d="m12 14 4-4"/><path d="M3.34 19a10 10 0 1 1 17.32 0"/>`)}
	puzzleIcon       = iconDef{src: iconWrap(`<path d="M20.5 11H19V7c0-1.1-.9-2-2-2h-4V3.5C13 2.12 11.88 1 10.5 1S8 2.12 8 3.5V5H4c-1.1 0-1.99.9-1.99 2v3.8H3.5c1.49 0 2.7 1.21 2.7 2.7s-1.21 2.7-2.7 2.7H2V20c0 1.1.9 2 2 2h3.8v-1.5c0-1.49 1.21-2.7 2.7-2.7s2.7 1.21 2.7 2.7V22H17c1.1 0 2-.9 2-2v-4h1.5c1.38 0 2.5-1.12 2.5-2.5S20.88 11 20.5 11z"/>`)}
	listChecksIcon   = iconDef{src: iconWrap(`<path d="m3 17 2 2 4-4"/><path d="m3 7 2 2 4-4"/><path d="M13 6h8"/><path d="M13 12h8"/><path d="M13 18h8"/>`)}
	fileDiffIcon     = iconDef{src: iconWrap(`<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M9 13h6"/><path d="M12 10v6"/>`)}
	shareIcon        = iconDef{src: iconWrap(`<circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><path d="M8.59 13.51 15.42 17.49"/><path d="M15.41 6.51 8.59 10.49"/>`)}
	activityIcon     = iconDef{src: iconWrap(`<path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2"/>`)}
	chartBarIcon     = iconDef{src: iconWrap(`<path d="M3 3v16a2 2 0 0 0 2 2h16"/><path d="M18 17V9"/><path d="M13 17V5"/><path d="M8 17v-3"/>`)}
	searchIcon       = iconDef{src: iconWrap(`<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>`)}
	refreshIcon      = iconDef{src: iconWrap(`<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>`)}
	copyIcon         = iconDef{src: iconWrap(`<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>`)}
	externalLinkIcon = iconDef{src: iconWrap(`<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>`)}
	checkIcon        = iconDef{src: iconWrap(`<path d="M20 6 9 17l-5-5"/>`)}
)

// IconGauge 返回“概览”图标（Lucide gauge）。
func IconGauge() *ui.SVG { return gaugeIcon.get() }

// IconPuzzle 返回“能力/插件”图标（Lucide puzzle）。
func IconPuzzle() *ui.SVG { return puzzleIcon.get() }

// IconListChecks 返回“任务”图标（Lucide list-checks）。
func IconListChecks() *ui.SVG { return listChecksIcon.get() }

// IconFileDiff 返回“文件变更”图标（Lucide file-diff）。
func IconFileDiff() *ui.SVG { return fileDiffIcon.get() }

// IconShare 返回“发布”图标（Lucide share-2）。
func IconShare() *ui.SVG { return shareIcon.get() }

// IconActivity 返回“追踪”图标（Lucide activity）。
func IconActivity() *ui.SVG { return activityIcon.get() }

// IconChartBar 返回“统计”图标（Lucide chart-column）。
func IconChartBar() *ui.SVG { return chartBarIcon.get() }

// IconSearch 返回搜索图标（Lucide search）。
func IconSearch() *ui.SVG { return searchIcon.get() }

// IconRefresh 返回刷新图标（Lucide refresh-cw）。
func IconRefresh() *ui.SVG { return refreshIcon.get() }

// IconCopy 返回复制图标（Lucide copy）。
func IconCopy() *ui.SVG { return copyIcon.get() }

// IconExternalLink 返回外链图标（Lucide external-link）。
func IconExternalLink() *ui.SVG { return externalLinkIcon.get() }

// IconCheck 返回对勾图标（Lucide check）。
func IconCheck() *ui.SVG { return checkIcon.get() }
