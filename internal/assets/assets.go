// Package assets 内嵌托盘图标等静态资源。
//
// @author ixxxxoooo
package assets

import _ "embed"

//go:embed tray.png
var tray []byte

// Tray 返回托盘模板图标（黑色透明底，由 macOS 按菜单栏明暗着色）。
//
// 返回值：PNG 字节（32×32，对应 16pt@2x）。
func Tray() []byte { return tray }
