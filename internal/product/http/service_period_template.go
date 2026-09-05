package http

import (
	"html/template"
	"time"
)

type servicePeriodPublicState struct {
	DonorStyle    template.CSS
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
<style>{{.DonorStyle}}</style></head>
<body><main class="service-period-page is-{{.Status}}" data-route-owner="ai_crm_next" data-fallback-used="false"><section class="service-period-hero">{{if eq .Status "active"}}<span class="service-period-tag" id="servicePeriodTag">服务中</span>{{else if eq .Status "expired"}}<span class="service-period-tag" id="servicePeriodTag">已过期</span>{{end}}<h1>{{.Product.Name}}</h1>{{if eq .Status "expired"}}<p id="servicePeriodHeroText">服务期已结束</p>{{end}}</section><section class="service-period-card" id="servicePeriodStateCard">{{if eq .Status "active"}}<div class="service-period-muted">剩余有效期</div><div class="service-period-state-big">{{.RemainingDays}} 天</div><div class="service-period-progress" aria-label="剩余服务期"><span style="width:100%"></span></div><div class="service-period-line"><span class="service-period-muted">到期日</span><strong>{{date .EndAt}}</strong></div><div class="service-period-line"><span class="service-period-muted">续费价格 / 有效期</span><strong>¥{{printf "%.2f" (cny .Product.PriceMinor)}} / {{.Product.ServicePeriodDurationDays}} 天</strong></div>{{else if eq .Status "expired"}}<div class="service-period-state-big">已过期</div><div class="service-period-line"><span class="service-period-muted">上次到期日</span><strong>{{date .EndAt}}</strong></div><div class="service-period-line"><span class="service-period-muted">重新开通价格 / 有效期</span><strong>¥{{printf "%.2f" (cny .Product.PriceMinor)}} / {{.Product.ServicePeriodDurationDays}} 天</strong></div>{{else}}<div class="service-period-price"><small>¥</small>{{printf "%.2f" (cny .Product.PriceMinor)}}</div><div class="service-period-line"><span class="service-period-muted">有效期</span><strong>{{.Product.ServicePeriodDurationDays}} 天</strong></div>{{end}}</section>{{if .Product.Images}}<section class="detail-media" aria-label="商品详情媒体">{{range .Product.Images}}<img class="slice-img" src="{{.}}" alt="商品详情">{{end}}</section>{{end}}</main><nav class="service-period-bar" aria-label="服务期商品操作"><div><div class="service-period-bar-title">{{.Product.Name}}</div><div class="service-period-bar-meta">{{if eq .Status "active"}}剩余 {{.RemainingDays}} 天{{else}}¥{{printf "%.2f" (cny .Product.PriceMinor)}} / {{.Product.ServicePeriodDurationDays}} 天{{end}}</div></div><button class="service-period-button" id="servicePeriodPayButton" type="button" onclick="location.href='{{.Product.PaymentPath}}'">{{.CTA}}</button></nav></body></html>`))
