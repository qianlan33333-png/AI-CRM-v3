package http

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// PublicHandler exposes only enabled Product facts. Draft, disabled and missing
// products intentionally share the same 404 response.
type PublicHandler struct {
	catalog PublicCatalogApplication
	media   publicProductMediaReader
}

type publicProductMediaReader interface {
	mediaport.ImageVariantReader
	LocalImageExists(context.Context, int64) (bool, error)
}

// PublicCatalogApplication is deliberately narrower than the admin catalog:
// public routes resolve a stable product code and cannot enumerate or mutate
// the catalog. Get is only used for pre-existing numeric public-link aliases.
type PublicCatalogApplication interface {
	Get(context.Context, productport.ID) (productport.Product, error)
	GetByCode(context.Context, string) (productport.Product, error)
}

type publicProduct struct {
	ID                        productport.ID `json:"id"`
	Name                      string         `json:"name"`
	Description               string         `json:"description"`
	PriceMinor                int64          `json:"price_minor"`
	Currency                  string         `json:"currency"`
	Images                    []string       `json:"images"`
	HeroURL                   string         `json:"-"`
	PaymentPath               string         `json:"-"`
	BuyButtonText             string         `json:"buy_button_text"`
	ProductKind               string         `json:"-"`
	ServicePeriodDurationDays int32          `json:"service_period_duration_days,omitempty"`
	CouponTargetRef           string         `json:"-"`
	RequireMobile             bool           `json:"require_mobile"`
}

func NewPublicHandler(catalog PublicCatalogApplication) (*PublicHandler, error) {
	if catalog == nil {
		return nil, errors.New("public product catalog is required")
	}
	return &PublicHandler{catalog: catalog}, nil
}

// SetPublicMediaReader supplies the existing Media read Port. The public
// product route independently checks that an enabled Product contains the
// exact image-library binding before it reads any bytes.
func (h *PublicHandler) SetPublicMediaReader(media publicProductMediaReader) error {
	if h == nil || media == nil {
		return errors.New("public product media reader is required")
	}
	h.media = media
	return nil
}

func (h *PublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.catalog == nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/h5/product-images/"):
		h.detailMedia(w, r)
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
	code, ok := publicProductCode(r, "/api/public/products/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	product, ok := h.enabledProduct(r, code)
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
	code, ok := publicProductCode(r, prefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	product, ok := h.enabledProduct(r, code)
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
	}{Product: product, Payment: true}
	if err := publicProductPage.Execute(w, data); err != nil {
		return
	}
}

func (h *PublicHandler) enabledProduct(r *http.Request, code string) (publicProduct, bool) {
	value, ok := h.enabledProductValue(r, code)
	if !ok {
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
	images, imageErr := productapp.PublicProductImageURLs(value)
	if imageErr != nil {
		return publicProduct{}, false
	}
	heroURL := ""
	if len(images) > 0 {
		heroURL = images[0]
	}
	return publicProduct{ID: value.ID, Name: value.Name, Description: value.Description, PriceMinor: value.PriceMinor, Currency: value.Currency, Images: images, HeroURL: heroURL, PaymentPath: "/pay/" + url.PathEscape(value.ProductCode), BuyButtonText: projection.BuyButtonText, ProductKind: "standard", CouponTargetRef: "standard_product:" + strconv.FormatInt(int64(value.ID), 10), RequireMobile: projection.RequireMobile}, true
}

func (h *PublicHandler) enabledProductValue(r *http.Request, code string) (productport.Product, bool) {
	value, err := h.catalog.GetByCode(r.Context(), code)
	if err != nil {
		legacyID, isLegacyID := legacyPublicProductID(code)
		if !isLegacyID || !errors.Is(err, productapp.ErrNotFound) {
			return productport.Product{}, false
		}
		value, err = h.catalog.Get(r.Context(), legacyID)
		if err != nil {
			return productport.Product{}, false
		}
	}
	local, err := productapp.ProjectLocalProduct(value)
	if err != nil || local.Lifecycle != productport.LocalProductEnabled || !local.Enabled {
		return productport.Product{}, false
	}
	return value, true
}

func (h *PublicHandler) detailMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" || h.media == nil {
		http.NotFound(w, r)
		return
	}
	const prefix = "/api/h5/product-images/"
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), prefix), "/")
	if len(parts) != 4 || parts[2] != "variants" || parts[3] != "original" {
		http.NotFound(w, r)
		return
	}
	code, err := url.PathUnescape(parts[0])
	id, idErr := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || code == "" || code != strings.TrimSpace(code) || len(code) > 200 || strings.ContainsRune(code, '\x00') || idErr != nil || id < 1 || strconv.FormatInt(id, 10) != parts[1] {
		http.NotFound(w, r)
		return
	}
	product, ok := h.enabledProductValue(r, code)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ids, idsErr := productapp.PublicProductImageIDs(product)
	if idsErr != nil || !containsImageID(ids, id) {
		http.NotFound(w, r)
		return
	}
	exists, existsErr := h.media.LocalImageExists(r.Context(), id)
	if existsErr != nil {
		http.Error(w, "media unavailable", http.StatusServiceUnavailable)
		return
	}
	if !exists {
		http.NotFound(w, r)
		return
	}
	variant, variantErr := h.media.GetImageVariant(r.Context(), id, "original")
	if variantErr != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", variant.MediaType)
	w.Header().Set("ETag", variant.ETag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(variant.Content)
}

func containsImageID(ids []int64, id int64) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// legacyPublicProductID recognizes only the numeric route format generated by
// the prior V3 public-sharing code. It is a read-only compatibility alias;
// every newly generated link uses product_code.
func legacyPublicProductID(code string) (productport.ID, bool) {
	id, err := strconv.ParseInt(code, 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return productport.ID(id), true
}

func publicProductCode(r *http.Request, prefix string) (string, bool) {
	escapedPath := r.URL.EscapedPath()
	if !strings.HasPrefix(escapedPath, prefix) {
		return "", false
	}
	escapedCode := strings.TrimPrefix(escapedPath, prefix)
	if escapedCode == "" || strings.Contains(escapedCode, "/") {
		return "", false
	}
	code, err := url.PathUnescape(escapedCode)
	if err != nil || code == "" || code != strings.TrimSpace(code) || len(code) > 200 || strings.ContainsRune(code, '\x00') {
		return "", false
	}
	return code, true
}

var publicProductPage = template.Must(template.New("public-product").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><title>{{.Product.Name}}</title>
<style>
*{box-sizing:border-box}body{margin:0;background:#f5f6f8;color:#20242b;font:15px/1.5 -apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif}.card{max-width:560px;margin:auto;padding:20px 16px calc(118px + env(safe-area-inset-bottom));min-height:100dvh}.panel{background:#fff;border-radius:18px;padding:20px;margin-bottom:14px}.auth-gate{min-height:calc(100dvh - 40px);display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center;padding:34px 24px}.auth-badge{display:inline-flex;align-items:center;height:28px;padding:0 12px;border-radius:999px;background:#eff4ff;color:#3268ff;font-size:13px;font-weight:600}.auth-gate h1{margin:18px 0 0;font-size:26px;line-height:1.3}.auth-message{max-width:22em;margin:12px auto 0;color:#858b95;font-size:14px;line-height:1.65}.auth-button{width:100%;min-height:48px;margin-top:26px;border:0;border-radius:12px;background:#3268ff;color:#fff;display:grid;place-items:center;font-size:16px;font-weight:600;text-decoration:none}.auth-button[aria-disabled="true"]{background:#a9bfff;pointer-events:none}.auth-note{margin-top:14px;color:#a1a6ae;font-size:12px}.auth-chevron{margin-top:16px;color:#3268ff;animation:auth-dip 1.6s ease-in-out infinite}@keyframes auth-dip{0%,100%{transform:translateY(0)}50%{transform:translateY(6px)}}.product-info{min-width:0}h1{font-size:19px;line-height:1.4;margin:0 0 5px;font-weight:650;overflow-wrap:anywhere}.desc{color:#9298a2;font-size:13px;white-space:pre-wrap;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}.price{font-size:22px;font-weight:650;margin-top:8px}.period{font-size:13px;color:#7b8390;margin-top:6px}.row{display:flex;align-items:center;justify-content:space-between;gap:14px;padding:17px 0;border-bottom:1px solid #f0f1f4}.row:first-child{padding-top:0}.row:last-child{padding-bottom:0;border:0}.label{color:#858b95;flex-shrink:0}.amount{font-variant-numeric:tabular-nums;white-space:nowrap}.total{font-size:21px;font-weight:600}.coupon-choice{min-width:0;text-align:right}.coupon{max-width:220px;width:100%;border:0;background:#fff;color:#56606e;font:inherit;text-align:right;outline-offset:4px}.discount{color:#df5b45;font-size:12px;margin-top:4px}.mobile{width:100%;height:46px;border:1px solid #e6e8ec;border-radius:10px;padding:0 12px;margin-top:12px;font:inherit;background:#fff}.mobile:focus{outline:2px solid #b4c8ff;border-color:#3268ff}.method{display:flex;align-items:center;gap:12px;margin-top:20px}.wechat-icon{width:34px;height:34px;background:#09b761;color:#fff;display:grid;place-items:center;border-radius:10px;font-size:21px}.selected{margin-left:auto;border-radius:50%;width:20px;height:20px;display:grid;place-items:center;background:#3268ff;color:#fff;font-size:13px}.notice{padding:12px;background:#fff5e7;color:#946728;border-radius:12px;margin-bottom:14px;font-size:13px}.checkout-footer{position:fixed;bottom:0;left:50%;transform:translateX(-50%);width:100%;max-width:560px;background:#fff;border-top:1px solid #ebedf0;padding:14px 18px calc(14px + env(safe-area-inset-bottom));display:flex;align-items:center;gap:18px;z-index:2}.footer-price{min-width:100px;flex:1}.footer-price .label{font-size:12px}.footer-price strong{display:block;font-size:24px;line-height:1.35;font-variant-numeric:tabular-nums}.buy{flex:1.1;min-height:48px;border:0;border-radius:13px;background:#3268ff;color:#fff;font-family:inherit;font-size:17px;font-weight:600;line-height:1.4;padding:12px 18px;cursor:pointer}.buy:disabled{background:#a9bfff;cursor:default}.restart{width:100%;margin-top:12px;background:#fff;color:#3268ff;border:1px solid #d8e2ff}.status{color:#7d8591;font-size:13px;text-align:center;overflow-wrap:anywhere;margin:14px 4px}.completion-qr{display:block;max-width:220px;width:100%;height:auto;margin:14px auto}.completion-title{font-weight:600;color:#20242b}.completion-subtitle{margin-top:4px}button:focus-visible,select:focus-visible,.auth-button:focus-visible{outline:3px solid #a6bfff;outline-offset:3px}[hidden]{display:none!important}@media(prefers-reduced-motion:reduce){.auth-chevron{animation:none}}@media(max-width:360px){.card{padding-left:12px;padding-right:12px}.panel{padding:16px}.coupon{max-width:180px}h1{font-size:17px}}
</style></head>
<body><main class="card"><section id="identityGate" class="panel auth-gate"><span class="auth-badge">微信身份验证</span><h1>完成微信授权后继续</h1><p id="identityMessage" class="auth-message">正在核验当前微信授权状态…</p><a id="authContinue" class="auth-button" href="#" hidden>微信授权后继续</a><svg class="auth-chevron" width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 9l6 6 6-6"></path></svg><p class="auth-note">授权成功后会自动返回当前页面</p></section><div id="checkoutContent" hidden><section class="panel product"><div class="product-info"><h1>{{.Product.Name}}</h1>{{with .Product.Description}}<div class="desc">{{.}}</div>{{end}}{{if gt .Product.ServicePeriodDurationDays 0}}<div class="period">服务周期 {{.Product.ServicePeriodDurationDays}} 天</div>{{end}}<div class="price amount">¥<span id="price"></span></div></div></section>
{{if .Payment}}<div id="wechatNotice" class="notice" hidden>请在微信内打开此页面完成支付。</div><section class="panel" aria-label="支付明细"><div class="row"><label class="label" for="coupon">优惠券</label><div class="coupon-choice"><select id="coupon" class="coupon"><option value="0">自动选择最优优惠券</option></select><div id="discountAmount" class="discount" hidden></div></div></div><div class="row"><span class="label">实付金额</span><strong class="amount total" id="payableAmount"></strong></div></section>
{{if .Product.RequireMobile}}<section class="panel"><label class="label" for="mobile">手机号</label><input id="mobile" class="mobile" type="tel" autocomplete="tel-national" inputmode="numeric" maxlength="11" placeholder="请输入手机号"></section>{{end}}
<section class="panel"><div class="label">支付方式</div><div class="method"><span class="wechat-icon" aria-hidden="true">✓</span><span>微信支付</span><span class="selected" aria-label="已选择微信支付">✓</span></div></section><div id="status" class="status" role="status" aria-live="polite"></div><button id="restart" class="buy restart" hidden>再次购买</button><footer class="checkout-footer"><div class="footer-price"><span class="label">实付</span><strong id="footerAmount"></strong></div><button id="buy" class="buy">立即支付</button></footer>{{end}}</div>
</main><script>document.getElementById('price').textContent=({{.Product.PriceMinor}}/100).toFixed(2);{{if .Payment}}
const button=document.getElementById('buy'),statusBox=document.getElementById('status'),couponField=document.getElementById('coupon'),identityGate=document.getElementById('identityGate'),identityMessage=document.getElementById('identityMessage'),authContinue=document.getElementById('authContinue'),checkoutContent=document.getElementById('checkoutContent'),inWechat=/MicroMessenger/i.test(navigator.userAgent),checkoutStorageKey='aicrm.checkout.v1:'+{{.Product.ID}}+':{{.Product.ProductKind}}';authContinue.href='/api/h5/wechat-pay/oauth/start?return_url='+encodeURIComponent(location.pathname)
function showIdentityGate(message){checkoutContent.hidden=true;identityGate.hidden=false;identityMessage.textContent=message;if(inWechat){authContinue.hidden=false;authContinue.removeAttribute('aria-disabled')}else{authContinue.hidden=false;authContinue.textContent='请在微信中打开';authContinue.setAttribute('aria-disabled','true')}}function revealCheckout(){identityGate.hidden=true;checkoutContent.hidden=false}
function checkoutPayload(value){if(!value||typeof value!=='object'||value.product_id!=={{.Product.ID}}||value.product_kind!=='{{.Product.ProductKind}}'||value.beneficiary_selection!=='payer_self'||!Number.isSafeInteger(value.coupon_claim_id)||value.coupon_claim_id<0)return null;if(value.mobile!==undefined&&(typeof value.mobile!=='string'||!/^\+861[3-9][0-9]{9}$/.test(value.mobile)))return null;const normalized={product_id:{{.Product.ID}},product_kind:'{{.Product.ProductKind}}',beneficiary_selection:'payer_self',coupon_claim_id:value.coupon_claim_id};if(value.mobile!==undefined)normalized.mobile=value.mobile;return normalized}
function checkoutBinding(value){return typeof value==='string'&&/^[A-Za-z0-9_-]{43}$/.test(value)?value:null}
function readCheckout(){try{const value=JSON.parse(localStorage.getItem(checkoutStorageKey)||'null'),payload=checkoutPayload(value&&value.payload),binding=checkoutBinding(value&&value.session_binding);if(!value||typeof value.key!=='string'||value.key.length<8||typeof value.merchant_order_no!=='string'||!payload||(value.terminal_status!==undefined&&value.terminal_status!=='paid'))return null;value.payload=payload;if(binding){value.session_binding=binding;return value}value.legacy_unbound=true;return value}catch(_){return null}}
function writeCheckout(value){try{localStorage.setItem(checkoutStorageKey,JSON.stringify(value));return true}catch(_){return false}}
function clearCheckout(){try{localStorage.removeItem(checkoutStorageKey)}catch(_){}}function retainPaidCheckout(orderNo){const checkpoint=readCheckout();if(!checkpoint||checkpoint.merchant_order_no!==orderNo)return;checkpoint.terminal_status='paid';writeCheckout(checkpoint)}
function checkoutKey(payload,binding){const existing=readCheckout();if(existing)return existing;const created={key:crypto.randomUUID(),merchant_order_no:'',payload:checkoutPayload(payload),session_binding:checkoutBinding(binding)};return created.payload&&created.session_binding&&writeCheckout(created)?created:null}
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));function requestFailure(code,message){const error=new Error(message);error.code=code;return error}async function requestJSON(url,options){const response=await fetch(url,options);let body={};try{body=await response.json()}catch(_){}if(response.status===401)throw requestFailure('payment_session_required','请先完成微信授权');if(!response.ok){const code=body.code||body.error||'';if(code==='payment_provider_disabled'||code==='payment_h5_oauth_disabled')throw requestFailure(code,'支付服务暂未启用');if(code==='session_mismatch')throw requestFailure(code,'付款授权已变化，原订单标识已保留；请恢复原授权后继续');if(code==='conflict')throw requestFailure(code,'商品或手机号状态不符合购买要求');throw requestFailure(code,'请求失败')}return body}
function showCompletionAction(action){if(!action||typeof action!=='object'||action.state!=='available')return;if(action.mode==='redirect'&&typeof action.redirect_url==='string'&&action.redirect_url){statusBox.textContent='支付成功，正在跳转…';location.assign(action.redirect_url);return}if(action.mode!=='qr'||!action.lead_qr||typeof action.lead_qr!=='object'||typeof action.lead_qr.url!=='string'||!action.lead_qr.url)return;const lead=action.lead_qr,image=document.createElement('img');image.className='completion-qr';image.src=lead.url;image.alt=typeof lead.title==='string'&&lead.title?lead.title:'二维码';statusBox.textContent='支付成功';statusBox.appendChild(image);if(typeof lead.title==='string'&&lead.title){const title=document.createElement('div');title.className='completion-title';title.textContent=lead.title;statusBox.appendChild(title)}if(typeof lead.subtitle==='string'&&lead.subtitle){const subtitle=document.createElement('div');subtitle.className='completion-subtitle';subtitle.textContent=lead.subtitle;statusBox.appendChild(subtitle)}}
function releaseExpiredCheckout(value,orderNo){if(value.checkout_restart_allowed!==true)return false;const current=readCheckout();if(!current||current.merchant_order_no!==orderNo)throw new Error('订单状态已更新，请刷新页面后继续');clearCheckout();if(readCheckout())throw new Error('无法清除已核验的旧订单记录，请重新打开页面');originalAmount=null;updateAmounts();restartButton.hidden=true;button.disabled=false;button.dataset.invoked='';statusBox.textContent='请填写手机号并确认支付';void loadCoupons().catch(error=>{if(error&&error.code==='payment_session_required')showIdentityGate('授权已失效，请重新完成微信授权。');else statusBox.textContent='优惠券读取失败，请稍后重试'});return true}
async function poll(orderNo){for(let i=0;i<90;i++){const value=await requestJSON('/api/v1/wechat-pay/checkouts/'+encodeURIComponent(orderNo),{credentials:'same-origin'});readOriginalAmount(value,orderNo);if(value.status==='paid'){retainPaidCheckout(orderNo);statusBox.textContent='支付成功';showCompletionAction(value.completion_action);if(value.completion_action&&value.completion_action.state==='unavailable')statusBox.textContent='支付成功，后续指引暂不可用';button.disabled=true;restartButton.hidden=false;return}if(releaseExpiredCheckout(value,orderNo))return;if(value.checkout_abandoned===true){statusBox.textContent='原支付流程已停止，订单记录已保留，尚未确认最终结果。请联系管理员核对后再购买。';button.disabled=true;restartButton.hidden=true;return}if(value.status==='failed'||value.status==='cancelled'){clearCheckout();originalAmount=null;updateAmounts();throw new Error('支付未完成，请确认后重新购买')}if(value.prepay_state==='outcome_unknown')throw new Error('微信支付下单结果尚未确认，原订单已保留，请稍后查看；请勿重复下单');if(value.prepay_state==='final_failed')throw new Error('微信支付暂不可用，原订单已保留，请联系客服处理');if(value.ready&&value.handoff&&!button.dataset.invoked){button.dataset.invoked='1';try{await invokePay(value.handoff)}catch(error){button.dataset.invoked='';throw error}}await sleep(1500)}button.dataset.invoked='';throw new Error('支付结果确认超时，请稍后刷新查看')}
function invokePay(handoff){return new Promise((resolve,reject)=>{let finished=false,timer;const finish=error=>{if(finished)return;finished=true;clearTimeout(timer);document.removeEventListener('WeixinJSBridgeReady',call);error?reject(error):resolve()};const call=()=>{if(finished)return;clearTimeout(timer);timer=setTimeout(()=>finish(new Error('微信支付结果尚未确认，请使用原订单继续查看')),120000);try{WeixinJSBridge.invoke('getBrandWCPayRequest',handoff,result=>finish(result&&result.err_msg==='get_brand_wcpay_request:ok'?null:new Error('支付未完成，请使用原订单继续支付')))}catch(_){finish(new Error('微信支付未能打开，请在微信中重新打开并继续原订单'))}};if(typeof WeixinJSBridge==='undefined'){timer=setTimeout(()=>finish(new Error('微信支付未能打开，请在微信中重新打开并继续原订单')),10000);document.addEventListener('WeixinJSBridgeReady',call,{once:true})}else call()})}
const couponDiscounts=new Map();let originalAmount=null;function readOriginalAmount(value,orderNo){originalAmount=value&&Number.isSafeInteger(value.amount_minor)&&value.amount_minor>0&&value.currency==='CNY'?{orderNo,amount:value.amount_minor}:null;updateAmounts()}function updateAmounts(){const checkpoint=readCheckout(),mobileField=document.getElementById('mobile');couponField.disabled=!!checkpoint;if(mobileField)mobileField.disabled=!!checkpoint;if(checkpoint){const amount=originalAmount&&originalAmount.orderNo===checkpoint.merchant_order_no?'¥'+(originalAmount.amount/100).toFixed(2):'待确认';document.getElementById('payableAmount').textContent=amount;document.getElementById('footerAmount').textContent=amount;document.getElementById('discountAmount').hidden=true;return}const gross={{.Product.PriceMinor}},selected=Number(couponField.value)||0;const discount=selected?(couponDiscounts.get(selected)||0):Math.max(0,...couponDiscounts.values());const money=value=>'¥'+(value/100).toFixed(2);document.getElementById('payableAmount').textContent=money(gross-discount);document.getElementById('footerAmount').textContent=money(gross-discount);const badge=document.getElementById('discountAmount');badge.hidden=!discount;badge.textContent=discount?'−'+money(discount):''}couponField.addEventListener('change',updateAmounts);updateAmounts();
async function loadCoupons(){if(!couponField||!inWechat||readCheckout())return;const value=await requestJSON('/api/h5/coupons/available?target_ref='+encodeURIComponent('{{.Product.CouponTargetRef}}'),{credentials:'same-origin'});couponDiscounts.clear();couponField.innerHTML='<option value="0">自动选择最优优惠券</option>';for(const item of value.items||[]){if(!Number.isSafeInteger(item.claim_id)||item.claim_id<1||typeof item.name!=='string'||!Number.isSafeInteger(item.discount_amount_minor)||item.discount_amount_minor<1||item.discount_amount_minor>={{.Product.PriceMinor}}||item.currency!=='CNY')continue;const option=document.createElement('option');option.value=String(item.claim_id);option.textContent=item.name+'（优惠 ¥'+(item.discount_amount_minor/100).toFixed(2)+'）';couponField.appendChild(option);couponDiscounts.set(item.claim_id,item.discount_amount_minor)}updateAmounts()}
async function currentCheckoutBinding(){const value=await requestJSON('/api/v1/wechat-pay/checkout-session',{credentials:'same-origin'}),binding=checkoutBinding(value.checkout_session_binding);if(!binding)throw requestFailure('unavailable','付款授权状态暂不可用');return binding}
const restartButton=document.getElementById('restart');restartButton.addEventListener('click',()=>{clearCheckout();originalAmount=null;updateAmounts();void loadCoupons().catch(error=>{if(error&&error.code==='payment_session_required')showIdentityGate('授权已失效，请重新完成微信授权。')});restartButton.hidden=true;button.disabled=false;statusBox.textContent='如需再次购买，请点击购买'});async function restorePaidCheckout(){const checkpoint=readCheckout();if(!checkpoint||!checkpoint.merchant_order_no)return;button.disabled=true;restartButton.hidden=true;statusBox.textContent='正在恢复支付结果…';try{const value=await requestJSON('/api/v1/wechat-pay/checkouts/'+encodeURIComponent(checkpoint.merchant_order_no),{credentials:'same-origin'});readOriginalAmount(value,checkpoint.merchant_order_no);if(value.status==='paid'){retainPaidCheckout(checkpoint.merchant_order_no);restartButton.hidden=false;statusBox.textContent='支付成功';showCompletionAction(value.completion_action);if(value.completion_action&&value.completion_action.state==='unavailable')statusBox.textContent='支付成功，后续指引暂不可用';return}if(checkpoint.terminal_status==='paid')throw new Error('付款状态暂未确认，请使用原订单继续确认');if(releaseExpiredCheckout(value,checkpoint.merchant_order_no))return;if(value.checkout_abandoned===true){statusBox.textContent='原支付流程已停止，订单记录已保留，尚未确认最终结果。请联系管理员核对后再购买。';return}button.disabled=false;statusBox.textContent='已恢复原订单，请继续确认支付。'}catch(error){if(error&&error.code==='payment_session_required'){showIdentityGate('授权已失效，请重新完成微信授权。');return}button.disabled=checkpoint.terminal_status==='paid';statusBox.textContent=error instanceof Error?error.message:'支付结果暂不可用'}}async function bootstrapCheckout(){if(!inWechat){showIdentityGate('请复制当前链接到微信中打开并完成授权。');return}try{await currentCheckoutBinding();revealCheckout()}catch(error){showIdentityGate(error&&error.code==='payment_session_required'?'授权后才能查看商品、优惠券并发起支付。':'授权状态暂时无法核验，请稍后重试。');return}void loadCoupons().catch(error=>{if(error&&error.code==='payment_session_required'){showIdentityGate('授权已失效，请重新完成微信授权。');return}statusBox.textContent=error instanceof Error?error.message:'优惠券读取失败'});void restorePaidCheckout()}
button.addEventListener('click',async()=>{button.disabled=true;let checkpoint=null;try{checkpoint=readCheckout();if(!checkpoint){let mobile='';const field=document.getElementById('mobile');if(field){mobile=field.value.trim();if(!/^1[3-9][0-9]{9}$/.test(mobile))throw new Error('请输入正确的大陆 11 位手机号')}const payload={product_id:{{.Product.ID}},product_kind:'{{.Product.ProductKind}}',beneficiary_selection:'payer_self',coupon_claim_id:Number(couponField&&couponField.value)||0};if(mobile)payload.mobile='+86'+mobile;checkpoint=checkoutKey(payload,await currentCheckoutBinding());if(!checkpoint)throw new Error('无法保存本次订单恢复信息，请检查浏览器存储后重试')}updateAmounts();if(checkpoint.legacy_unbound){if(!checkpoint.merchant_order_no)throw requestFailure('legacy_checkpoint_unbound','旧版订单恢复标识缺少付款会话绑定，已保留原标识，请勿重新下单');statusBox.textContent='正在恢复原订单…';await poll(checkpoint.merchant_order_no);return}if(checkpoint.merchant_order_no){statusBox.textContent='正在恢复原订单…';await poll(checkpoint.merchant_order_no);return}const currentBinding=await currentCheckoutBinding();if(currentBinding!==checkpoint.session_binding)throw requestFailure('session_mismatch','付款授权已变化，原订单标识已保留；请恢复原授权后继续');statusBox.textContent='正在恢复原订单…';const created=await requestJSON('/api/v1/wechat-pay/checkouts',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','Idempotency-Key':checkpoint.key},body:JSON.stringify({...checkpoint.payload,checkout_session_binding:checkpoint.session_binding})});checkpoint.merchant_order_no=created.merchant_order_no;if(!writeCheckout(checkpoint))throw new Error('无法保存原订单恢复信息，请勿重新下单');statusBox.textContent='等待微信支付…';await poll(created.merchant_order_no)}catch(error){if(error&&error.code==='payment_session_required'){showIdentityGate('本次微信授权已失效，请重新授权后继续。');return}statusBox.textContent=error instanceof Error?error.message:'支付结果尚未确认，请使用原订单重试';button.disabled=false}});void bootstrapCheckout();{{end}}</script></body></html>`))
