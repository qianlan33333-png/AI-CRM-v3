package http

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// PublicHandler exposes only enabled Product facts. Draft, disabled and missing
// products intentionally share the same 404 response.
type PublicHandler struct {
	catalog CatalogApplication
}

type publicProduct struct {
	ID            productport.ID `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	PriceMinor    int64          `json:"price_minor"`
	Currency      string         `json:"currency"`
	Images        []string       `json:"images"`
	HeroURL       string         `json:"-"`
	BuyButtonText string         `json:"buy_button_text"`
	RequireMobile bool           `json:"require_mobile"`
}

func NewPublicHandler(catalog CatalogApplication) (*PublicHandler, error) {
	if catalog == nil {
		return nil, errors.New("public product catalog is required")
	}
	return &PublicHandler{catalog: catalog}, nil
}

func (h *PublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.catalog == nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/public/products/"):
		h.publicAPI(w, r)
	case strings.HasPrefix(r.URL.Path, "/p/"):
		h.publicPage(w, r, false)
	case strings.HasPrefix(r.URL.Path, "/pay/"):
		h.publicPage(w, r, true)
	default:
		http.NotFound(w, r)
	}
}

func (h *PublicHandler) publicAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	product, ok := h.enabledProduct(r, strings.TrimPrefix(r.URL.Path, "/api/public/products/"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, product)
}

func (h *PublicHandler) publicPage(w http.ResponseWriter, r *http.Request, payment bool) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	prefix := "/p/"
	if payment {
		prefix = "/pay/"
	}
	product, ok := h.enabledProduct(r, strings.TrimPrefix(r.URL.Path, prefix))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https: data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	data := struct {
		Product publicProduct
		Payment bool
	}{Product: product, Payment: payment}
	if err := publicProductPage.Execute(w, data); err != nil {
		return
	}
}

func (h *PublicHandler) enabledProduct(r *http.Request, rawID string) (publicProduct, bool) {
	if strings.Contains(rawID, "/") {
		return publicProduct{}, false
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		return publicProduct{}, false
	}
	value, err := h.catalog.Get(r.Context(), productport.ID(id))
	if err != nil {
		return publicProduct{}, false
	}
	local, err := productapp.ProjectLocalProduct(value)
	if err != nil || local.Lifecycle != productport.LocalProductEnabled || !local.Enabled {
		return publicProduct{}, false
	}
	var projection struct {
		BuyButtonText string `json:"buy_button_text"`
		RequireMobile bool   `json:"require_mobile"`
	}
	if json.Unmarshal(value.LegacyAdminProjection, &projection) != nil {
		return publicProduct{}, false
	}
	if strings.TrimSpace(projection.BuyButtonText) == "" {
		projection.BuyButtonText = "立即购买"
	}
	heroURL := ""
	if len(value.Images) > 0 {
		heroURL = value.Images[0]
	}
	return publicProduct{ID: value.ID, Name: value.Name, Description: value.Description, PriceMinor: value.PriceMinor, Currency: value.Currency, Images: append([]string(nil), value.Images...), HeroURL: heroURL, BuyButtonText: projection.BuyButtonText, RequireMobile: projection.RequireMobile}, true
}

var publicProductPage = template.Must(template.New("public-product").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><title>{{.Product.Name}}</title>
<style>body{margin:0;background:#f5f6f8;color:#1f2329;font:16px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.card{max-width:560px;margin:0 auto;min-height:100vh;background:#fff}.hero{display:block;width:100%;max-height:420px;object-fit:cover}.body{padding:24px}.price{color:#d83931;font-size:30px;font-weight:700;margin:12px 0}.desc{white-space:pre-wrap;color:#4e5969}.notice{padding:12px;border-radius:8px;background:#fff7e8;color:#8f4f00;margin:16px 0}.mobile{box-sizing:border-box;width:100%;height:46px;border:1px solid #c9cdd4;border-radius:8px;padding:0 12px;margin:8px 0}.buy{box-sizing:border-box;width:100%;height:48px;border:0;border-radius:8px;background:#245bdb;color:#fff;font-size:17px;font-weight:600}.buy:disabled{background:#bbb}.status{margin-top:14px;color:#646a73;text-align:center}</style></head>
<body><main class="card">{{with .Product.HeroURL}}<img class="hero" src="{{.}}" alt="商品图片">{{end}}<div class="body"><h1>{{.Product.Name}}</h1><div class="price">¥<span id="price"></span></div><div class="desc">{{.Product.Description}}</div>
{{if .Payment}}<div id="wechatNotice" class="notice" hidden>请在微信内打开此页面完成支付。</div>{{if .Product.RequireMobile}}<label for="mobile">手机号</label><input id="mobile" class="mobile" inputmode="numeric" maxlength="11" placeholder="请输入大陆 11 位手机号">{{end}}<button id="buy" class="buy">{{.Product.BuyButtonText}}</button><div id="status" class="status"></div>{{else}}<button id="buy" class="buy" onclick="location.href='/pay/{{.Product.ID}}'">{{.Product.BuyButtonText}}</button>{{end}}
</div></main><script>document.getElementById('price').textContent=({{.Product.PriceMinor}}/100).toFixed(2);{{if .Payment}}
const button=document.getElementById('buy'),statusBox=document.getElementById('status'),inWechat=/MicroMessenger/i.test(navigator.userAgent);if(!inWechat){document.getElementById('wechatNotice').hidden=false;button.disabled=true}
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));async function requestJSON(url,options){const response=await fetch(url,options);let body={};try{body=await response.json()}catch(_){}if(response.status===401){location.href='/api/h5/wechat-pay/oauth/start?return_url='+encodeURIComponent(location.pathname);throw new Error('正在进入微信授权')}if(!response.ok){const code=body.code||body.error||'';if(code==='payment_provider_disabled'||code==='payment_h5_oauth_disabled')throw new Error('支付服务暂未启用');if(code==='conflict')throw new Error('商品或手机号状态不符合购买要求');throw new Error('请求失败')}return body}
async function poll(orderNo){for(let i=0;i<90;i++){const value=await requestJSON('/api/v1/wechat-pay/checkouts/'+encodeURIComponent(orderNo),{credentials:'same-origin'});if(value.status==='paid'){statusBox.textContent='支付成功';button.disabled=true;return}if(value.status==='failed'||value.status==='cancelled')throw new Error('支付未完成');if(value.ready&&value.handoff&&!button.dataset.invoked){button.dataset.invoked='1';await invokePay(value.handoff)}await sleep(1500)}throw new Error('支付结果确认超时，请稍后刷新查看')}
function invokePay(handoff){return new Promise((resolve,reject)=>{const call=()=>WeixinJSBridge.invoke('getBrandWCPayRequest',handoff,result=>result.err_msg==='get_brand_wcpay_request:ok'?resolve():reject(new Error('支付未完成')));if(typeof WeixinJSBridge==='undefined')document.addEventListener('WeixinJSBridgeReady',call,{once:true});else call()})}
button.addEventListener('click',async()=>{button.disabled=true;statusBox.textContent='正在创建订单…';try{let mobile='';const field=document.getElementById('mobile');if(field){mobile=field.value.trim();if(!/^1[3-9][0-9]{9}$/.test(mobile))throw new Error('请输入正确的大陆 11 位手机号')}const payload={product_id:{{.Product.ID}},product_kind:'standard'};if(mobile)payload.mobile='+86'+mobile;const created=await requestJSON('/api/v1/wechat-pay/checkouts',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:JSON.stringify(payload)});statusBox.textContent='等待微信支付…';await poll(created.merchant_order_no)}catch(error){statusBox.textContent=error instanceof Error?error.message:'支付失败';button.disabled=false}});{{end}}</script></body></html>`))
