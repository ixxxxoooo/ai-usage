// 命令 genicon 用纯 Go 绘制应用图标与托盘模板图标，输出 PNG 文件。
//
// 用法：go run ./cmd/genicon
//
// @author ygw
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

// ss 是超采样倍数，用于抗锯齿。
const ss = 4

// coverage 对 [x,x+1)×[y,y+1) 的像素做 ss×ss 超采样，返回 inside 为真的比例。
func coverage(x, y int, inside func(px, py float64) bool) float64 {
	n := 0
	for sy := 0; sy < ss; sy++ {
		for sx := 0; sx < ss; sx++ {
			px := float64(x) + (float64(sx)+0.5)/ss
			py := float64(y) + (float64(sy)+0.5)/ss
			if inside(px, py) {
				n++
			}
		}
	}
	return float64(n) / (ss * ss)
}

// arcRing 判断点是否落在以 (cx,cy) 为圆心、内外半径为 r0/r1 的圆环上，
// 且角度（以正上方为 0，顺时针）位于从 a0 起到 a1 的扇区内（单位：度，a1>a0，可超过 360）。
func arcRing(px, py, cx, cy, r0, r1, a0, a1 float64) bool {
	dx, dy := px-cx, py-cy
	d := math.Hypot(dx, dy)
	if d < r0 || d > r1 {
		return false
	}
	ang := math.Atan2(dx, -dy) * 180 / math.Pi // 正上方为 0，顺时针为正
	rel := math.Mod(ang-a0+720, 360)           // 相对起点 a0 顺时针转过的角度
	return rel <= a1-a0
}

// capDot 判断点是否在圆弧两端的圆头里，使圆弧末端呈圆角。
func capDot(px, py, cx, cy, rm, w, angle float64) bool {
	a := angle * math.Pi / 180
	ex, ey := cx+rm*math.Sin(a), cy-rm*math.Cos(a)
	return math.Hypot(px-ex, py-ey) <= w/2
}

// drawTray 绘制托盘模板图标：黑色透明底（macOS 会按菜单栏明暗自动着色）。
// 图形是一个 270° 的仪表圆环，并在其中带一个向上的小条形。
func drawTray(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	cx, cy := s/2, s/2
	rm, w := s*0.36, s*0.12
	r0, r1 := rm-w/2, rm+w/2
	a0, a1 := 225.0, 495.0 // 开口朝下的 270° 圆弧（从左下起顺时针，经正上方到右下）
	shape := func(px, py float64) bool {
		if arcRing(px, py, cx, cy, r0, r1, a0, a1) || capDot(px, py, cx, cy, rm, w, a0) || capDot(px, py, cx, cy, rm, w, a1) {
			return true
		}
		// 三根递增的小条形
		bw := s * 0.085
		gap := s * 0.055
		base := cy + s*0.17
		for i := 0; i < 3; i++ {
			h := s * (0.12 + 0.08*float64(i))
			x0 := cx - (1.5*bw + gap) + float64(i)*(bw+gap)
			if px >= x0 && px <= x0+bw && py <= base && py >= base-h {
				return true
			}
		}
		return false
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if c := coverage(x, y, shape); c > 0 {
				img.SetNRGBA(x, y, color.NRGBA{A: uint8(math.Round(c * 255))})
			}
		}
	}
	return img
}

// lerp 线性插值。
func lerp(a, b, t float64) float64 { return a + (b-a)*t }

// drawApp 绘制 1024 的应用图标：蓝紫渐变圆角方块 + 白色仪表圆环 + 强调色圆弧。
func drawApp(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	margin := s * 0.09
	half := (s - 2*margin) / 2
	cx, cy := s/2, s/2
	squircle := func(px, py float64) bool {
		x := math.Abs(px-cx) / half
		y := math.Abs(py-cy) / half
		return math.Pow(x, 5)+math.Pow(y, 5) <= 1
	}
	rm, w := s*0.27, s*0.075
	r0, r1 := rm-w/2, rm+w/2
	// 底环：白色半透明 270°；进度弧：亮青色 0~72%（与真实用量 359/500 相呼应）
	progEnd := 225.0 + 270*0.72
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			cov := coverage(x, y, squircle)
			if cov == 0 {
				continue
			}
			// 对角渐变背景
			t := (float64(x) + float64(y)) / (2 * s)
			bg := [3]float64{lerp(88, 34, t), lerp(86, 52, t), lerp(214, 160, t)}
			px, py := float64(x)+0.5, float64(y)+0.5
			col := bg
			mix := func(c [3]float64, a float64) {
				for i := range col {
					col[i] = col[i]*(1-a) + c[i]*a
				}
			}
			ring := coverage(x, y, func(px, py float64) bool {
				return arcRing(px, py, cx, cy, r0, r1, 225, 495) || capDot(px, py, cx, cy, rm, w, 225) || capDot(px, py, cx, cy, rm, w, 495)
			})
			mix([3]float64{255, 255, 255}, ring*0.28)
			prog := coverage(x, y, func(px, py float64) bool {
				return arcRing(px, py, cx, cy, r0, r1, 225, progEnd) || capDot(px, py, cx, cy, rm, w, 225) || capDot(px, py, cx, cy, rm, w, progEnd)
			})
			mix([3]float64{120, 245, 220}, prog)
			// 中央递增条形
			bars := coverage(x, y, func(px, py float64) bool {
				bw, gap := s*0.06, s*0.04
				base := cy + s*0.12
				for i := 0; i < 3; i++ {
					h := s * (0.09 + 0.06*float64(i))
					x0 := cx - (1.5*bw + gap) + float64(i)*(bw+gap)
					if px >= x0 && px <= x0+bw && py <= base && py >= base-h {
						return true
					}
				}
				return false
			})
			mix([3]float64{255, 255, 255}, bars)
			_ = px
			_ = py
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(col[0]), G: uint8(col[1]), B: uint8(col[2]), A: uint8(math.Round(cov * 255))})
		}
	}
	return img
}

// save 把图像写成 PNG。
func save(path string, img image.Image) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	log.Println("已生成", path)
}

// main 生成全部图标资源。
func main() {
	save("resources/icon.png", drawApp(1024))
	save("internal/assets/tray.png", drawTray(32)) // 16pt @2x
}
