package http

import (
	"html/template"
	"time"
)

type servicePeriodPublicState struct {
	Product       publicProduct
	Status        string
	CTA           string
	EndAt         time.Time
	RemainingDays int32
}

// This Host template keeps the donor service-period page's route, state-card,
// status labels, fixed action bar and element IDs. Product and Order provide
// only the dynamic facts; no legacy runtime or business implementation runs.
var servicePeriodPublicPage = template.Must(template.New("service-period-public").Funcs(template.FuncMap{
	"cny": func(value int64) float64 { return float64(value) / 100 },
	"date": func(value time.Time) string {
		if value.IsZero() {
			return "-"
		}
		return value.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
	},
}).Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1"><title>{{.Product.Name}}</title>
<style>*{box-sizing:border-box}html,body{margin:0;min-height:100%;background:#f7f8fa;color:#1f2329;font:14px/1.45 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",Arial,sans-serif}.service-period-page{width:min(100%,750px);min-height:100vh;margin:0 auto;padding-bottom:92px;background:#f5f6f7}.service-period-hero{min-height:154px;padding:28px 20px 34px;background:linear-gradient(135deg,#3370ff,#245bdb);color:#fff}.service-period-tag{display:inline-flex;min-height:24px;padding:3px 9px;border-radius:4px;background:rgba(255,255,255,.14);font-size:12px;margin-bottom:14px}.service-period-hero h1{margin:0;font-size:24px;line-height:1.24;font-weight:900}.service-period-hero p{margin:8px 0 0;color:rgba(255,255,255,.72)}.service-period-card{position:relative;margin:-16px 14px 12px;padding:18px 16px;border:1px solid #dee0e3;border-radius:16px;background:#fff;box-shadow:0 8px 24px rgba(31,35,41,.10)}.service-period-price{font-size:32px;line-height:1.1;font-weight:950}.service-period-price small{font-size:16px;margin-right:2px}.service-period-state-big{font-size:38px;line-height:1.1;font-weight:950}.service-period-line{display:flex;justify-content:space-between;gap:10px;padding:10px 0;border-bottom:1px solid #f0f1f2}.service-period-line:last-child{border-bottom:0}.service-period-progress{height:8px;margin:14px 0 8px;overflow:hidden;border-radius:999px;background:#eff0f1}.service-period-progress span{display:block;height:100%;border-radius:inherit;background:#2ea121}.service-period-muted{color:#8f959e}.service-period-bar{position:fixed;left:50%;bottom:0;z-index:20;width:min(100%,750px);transform:translateX(-50%);display:grid;grid-template-columns:minmax(0,1fr) 132px;gap:12px;align-items:center;padding:10px 14px 12px;border-top:1px solid #dee0e3;background:rgba(255,255,255,.96)}.service-period-bar-title{font-size:13px;font-weight:900;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.service-period-bar-meta{margin-top:3px;color:#8f959e;font-size:12px}.service-period-button{height:50px;border:0;border-radius:12px;background:#3370ff;color:#fff;font-weight:900;cursor:pointer}</style></head>
<body><main class="service-period-page is-{{.Status}}" data-route-owner="ai_crm_next" data-fallback-used="false"><section class="service-period-hero">{{if eq .Status "active"}}<span class="service-period-tag" id="servicePeriodTag">服务中</span>{{else if eq .Status "expired"}}<span class="service-period-tag" id="servicePeriodTag">已过期</span>{{end}}<h1>{{.Product.Name}}</h1>{{if eq .Status "expired"}}<p id="servicePeriodHeroText">服务期已结束</p>{{end}}</section><section class="service-period-card" id="servicePeriodStateCard">{{if eq .Status "active"}}<div class="service-period-muted">剩余有效期</div><div class="service-period-state-big">{{.RemainingDays}} 天</div><div class="service-period-progress" aria-label="剩余服务期"><span style="width:100%"></span></div><div class="service-period-line"><span class="service-period-muted">到期日</span><strong>{{date .EndAt}}</strong></div><div class="service-period-line"><span class="service-period-muted">续费价格 / 有效期</span><strong>¥{{printf "%.2f" (cny .Product.PriceMinor)}} / {{.Product.ServicePeriodDurationDays}} 天</strong></div>{{else if eq .Status "expired"}}<div class="service-period-state-big">已过期</div><div class="service-period-line"><span class="service-period-muted">上次到期日</span><strong>{{date .EndAt}}</strong></div><div class="service-period-line"><span class="service-period-muted">重新开通价格 / 有效期</span><strong>¥{{printf "%.2f" (cny .Product.PriceMinor)}} / {{.Product.ServicePeriodDurationDays}} 天</strong></div>{{else}}<div class="service-period-price"><small>¥</small>{{printf "%.2f" (cny .Product.PriceMinor)}}</div><div class="service-period-line"><span class="service-period-muted">有效期</span><strong>{{.Product.ServicePeriodDurationDays}} 天</strong></div>{{end}}</section></main><nav class="service-period-bar" aria-label="服务期商品操作"><div><div class="service-period-bar-title">{{.Product.Name}}</div><div class="service-period-bar-meta">{{if eq .Status "active"}}剩余 {{.RemainingDays}} 天{{else}}¥{{printf "%.2f" (cny .Product.PriceMinor)}} / {{.Product.ServicePeriodDurationDays}} 天{{end}}</div></div><button class="service-period-button" id="servicePeriodPayButton" type="button" onclick="location.href='{{.Product.PaymentPath}}'">{{.CTA}}</button></nav></body></html>`))
