package gui

import (
	"time"

	"github.com/liwenjiao/ai-usage/internal/model"
	"github.com/liwenjiao/ai-usage/internal/store"
)

// maxEvents 是界面最多加载的 Cursor 明细条数（按时间倒序截取）。
const maxEvents = 50000

// Data 是界面用的明细数据快照；整体替换、只读，因此可以安全跨 goroutine 读取。
//
// @author ygw
type Data struct {
	Events   []model.CursorEvent     // Cursor 明细，时间倒序
	Models   []string                // Cursor 出现过的模型
	Days     []model.CursorDayStat   // Cursor 按日汇总（升序）
	ByModel  []model.CursorModelStat // Cursor 按模型汇总（次数降序）
	Daily    []model.DeepSeekDaily   // DeepSeek 账单明细（日期升序）
	DSDays   []model.DeepSeekDayStat // DeepSeek 按日汇总（升序）
	LoadedAt time.Time
}

// LoadData 从存储读取明细并做汇总。
//
// 参数 st：存储；now：当前时间。
// 返回值：Data；读取失败时返回已读到的部分与错误。
func LoadData(st *store.Store, now time.Time) (Data, error) {
	d := Data{LoadedAt: now}
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	evs, err := st.CursorEvents(store.EventQuery{Limit: maxEvents})
	keep(err)
	d.Events = evs
	d.Models, err = st.CursorModels()
	keep(err)
	d.Days = model.AggregateCursorByDay(evs, now.Location())
	d.ByModel = model.AggregateCursorByModel(evs)
	d.Daily, err = st.DeepSeekDaily("", "")
	keep(err)
	d.DSDays = model.AggregateDeepSeekByDay(d.Daily)
	return d, firstErr
}
