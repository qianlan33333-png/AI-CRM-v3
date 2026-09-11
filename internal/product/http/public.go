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
	}{Product: product, Payment: payment}
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
<style>body{margin:0;background:#f5f6f8;color:#1f2329;font:16px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.card{max-width:560px;margin:0 auto;min-height:100vh;background:#fff}.hero{display:block;width:100%;max-height:420px;object-fit:cover}.body{padding:24px}.price{color:#d83931;font-size:30px;font-weight:700;margin:12px 0}.desc{white-space:pre-wrap;color:#4e5969}.notice{padding:12px;border-radius:8px;background:#fff7e8;color:#8f4f00;margin:16px 0}.mobile{box-sizing:border-box;width:100%;height:46px;border:1px solid #c9cdd4;border-radius:8px;padding:0 12px;margin:8px 0}.beneficiary{display:block;margin:16px 0;color:#4e5969}.buy{box-sizing:border-box;width:100%;height:48px;border:0;border-radius:8px;background:#245bdb;color:#fff;font-size:17px;font-weight:600}.buy:disabled{background:#bbb}.restart{margin-top:12px;background:#fff;color:#245bdb;border:1px solid #245bdb}.status{margin-top:14px;color:#646a73;text-align:center}.completion-qr{display:block;max-width:220px;width:100%;height:auto;margin:14px auto 0}.completion-title{margin-top:12px;font-weight:600;color:#1f2329}.completion-subtitle{margin-top:4px;color:#646a73}</style></head>
<body><main class="card">{{with .Product.HeroURL}}<img class="hero" src="{{.}}" alt="商品图片">{{end}}<div class="body"><h1>{{.Product.Name}}</h1><div class="price">¥<span id="price"></span></div>{{if gt .Product.ServicePeriodDurationDays 0}}<div class="notice">服务周期：{{.Product.ServicePeriodDurationDays}} 天</div>{{end}}<div class="desc">{{.Product.Description}}</div>
{{if .Payment}}<div id="wechatNotice" class="notice" hidden>请在微信内打开此页面完成支付。</div>{{if .Product.RequireMobile}}<label for="mobile">手机号</label><input id="mobile" class="mobile" inputmode="numeric" maxlength="11" placeholder="请输入大陆 11 位手机号">{{end}}<label for="coupon">优惠券</label><select id="coupon" class="mobile"><option value="0">自动选择最优优惠券</option></select><label class="beneficiary" for="beneficiarySelf"><input id="beneficiarySelf" type="checkbox"> 我确认购买后权益归我本人</label><button id="buy" class="buy">{{.Product.BuyButtonText}}</button><button id="restart" class="buy restart" hidden>再次购买</button><div id="status" class="status"></div>{{else}}<button id="buy" class="buy" onclick="location.href='{{.Product.PaymentPath}}'">{{.Product.BuyButtonText}}</button>{{end}}
</div></main><script>document.getElementById('price').textContent=({{.Product.PriceMinor}}/100).toFixed(2);{{if .Payment}}
const button=document.getElementById('buy'),statusBox=document.getElementById('status'),couponField=document.getElementById('coupon'),inWechat=/MicroMessenger/i.test(navigator.userAgent),checkoutStorageKey='aicrm.checkout.v1:'+{{.Product.ID}}+':{{.Product.ProductKind}}';if(!inWechat){document.getElementById('wechatNotice').hidden=false;button.disabled=true}
function checkoutPayload(value){if(!value||typeof value!=='object'||value.product_id!=={{.Product.ID}}||value.product_kind!=='{{.Product.ProductKind}}'||value.beneficiary_selection!=='payer_self'||!Number.isSafeInteger(value.coupon_claim_id)||value.coupon_claim_id<0)return null;if(value.mobile!==undefined&&(typeof value.mobile!=='string'||!/^\+861[3-9][0-9]{9}$/.test(value.mobile)))return null;const normalized={product_id:{{.Product.ID}},product_kind:'{{.Product.ProductKind}}',beneficiary_selection:'payer_self',coupon_claim_id:value.coupon_claim_id};if(value.mobile!==undefined)normalized.mobile=value.mobile;return normalized}
function checkoutBinding(value){return typeof value==='string'&&/^[A-Za-z0-9_-]{43}$/.test(value)?value:null}
function readCheckout(){try{const value=JSON.parse(localStorage.getItem(checkoutStorageKey)||'null'),payload=checkoutPayload(value&&value.payload),binding=checkoutBinding(value&&value.session_binding);if(!value||typeof value.key!=='string'||value.key.length<8||typeof value.merchant_order_no!=='string'||!payload||(value.terminal_status!==undefined&&value.terminal_status!=='paid'))return null;value.payload=payload;if(binding){value.session_binding=binding;return value}value.legacy_unbound=true;return value}catch(_){return null}}
function writeCheckout(value){try{localStorage.setItem(checkoutStorageKey,JSON.stringify(value));return true}catch(_){return false}}
function clearCheckout(){try{localStorage.removeItem(checkoutStorageKey)}catch(_){}}function retainPaidCheckout(orderNo){const checkpoint=readCheckout();if(!checkpoint||checkpoint.merchant_order_no!==orderNo)return;checkpoint.terminal_status='paid';writeCheckout(checkpoint)}
function checkoutKey(payload,binding){const existing=readCheckout();if(existing)return existing;const created={key:crypto.randomUUID(),merchant_order_no:'',payload:checkoutPayload(payload),session_binding:checkoutBinding(binding)};return created.payload&&created.session_binding&&writeCheckout(created)?created:null}
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));function requestFailure(code,message){const error=new Error(message);error.code=code;return error}async function requestJSON(url,options){const response=await fetch(url,options);let body={};try{body=await response.json()}catch(_){}if(response.status===401){location.href='/api/h5/wechat-pay/oauth/start?return_url='+encodeURIComponent(location.pathname);throw requestFailure('payment_session_required','正在进入微信授权')}if(!response.ok){const code=body.code||body.error||'';if(code==='payment_provider_disabled'||code==='payment_h5_oauth_disabled')throw requestFailure(code,'支付服务暂未启用');if(code==='session_mismatch')throw requestFailure(code,'付款授权已变化，原订单标识已保留；请恢复原授权后继续');if(code==='conflict')throw requestFailure(code,'商品或手机号状态不符合购买要求');throw requestFailure(code,'请求失败')}return body}
function showCompletionAction(action){if(!action||typeof action!=='object'||action.state!=='available')return;if(action.mode==='redirect'&&typeof action.redirect_url==='string'&&action.redirect_url){statusBox.textContent='支付成功，正在跳转…';location.assign(action.redirect_url);return}if(action.mode!=='qr'||!action.lead_qr||typeof action.lead_qr!=='object'||typeof action.lead_qr.url!=='string'||!action.lead_qr.url)return;const lead=action.lead_qr,image=document.createElement('img');image.className='completion-qr';image.src=lead.url;image.alt=typeof lead.title==='string'&&lead.title?lead.title:'二维码';statusBox.textContent='支付成功';statusBox.appendChild(image);if(typeof lead.title==='string'&&lead.title){const title=document.createElement('div');title.className='completion-title';title.textContent=lead.title;statusBox.appendChild(title)}if(typeof lead.subtitle==='string'&&lead.subtitle){const subtitle=document.createElement('div');subtitle.className='completion-subtitle';subtitle.textContent=lead.subtitle;statusBox.appendChild(subtitle)}}
async function poll(orderNo){for(let i=0;i<90;i++){const value=await requestJSON('/api/v1/wechat-pay/checkouts/'+encodeURIComponent(orderNo),{credentials:'same-origin'});if(value.status==='paid'){retainPaidCheckout(orderNo);statusBox.textContent='支付成功';showCompletionAction(value.completion_action);if(value.completion_action&&value.completion_action.state==='unavailable')statusBox.textContent='支付成功，后续指引暂不可用';button.disabled=true;restartButton.hidden=false;return}if(value.status==='failed'||value.status==='cancelled'){clearCheckout();throw new Error('支付未完成，请确认后重新购买')}if(value.prepay_state==='outcome_unknown')throw new Error('微信支付下单结果尚未确认，原订单已保留，请稍后查看；请勿重复下单');if(value.prepay_state==='final_failed')throw new Error('微信支付暂不可用，原订单已保留，请联系客服处理');if(value.ready&&value.handoff&&!button.dataset.invoked){button.dataset.invoked='1';try{await invokePay(value.handoff)}catch(error){button.dataset.invoked='';throw error}}await sleep(1500)}button.dataset.invoked='';throw new Error('支付结果确认超时，请稍后刷新查看')}
function invokePay(handoff){return new Promise((resolve,reject)=>{let finished=false,timer;const finish=error=>{if(finished)return;finished=true;clearTimeout(timer);document.removeEventListener('WeixinJSBridgeReady',call);error?reject(error):resolve()};const call=()=>{if(finished)return;clearTimeout(timer);timer=setTimeout(()=>finish(new Error('微信支付结果尚未确认，请使用原订单继续查看')),120000);try{WeixinJSBridge.invoke('getBrandWCPayRequest',handoff,result=>finish(result&&result.err_msg==='get_brand_wcpay_request:ok'?null:new Error('支付未完成，请使用原订单继续支付')))}catch(_){finish(new Error('微信支付未能打开，请在微信中重新打开并继续原订单'))}};if(typeof WeixinJSBridge==='undefined'){timer=setTimeout(()=>finish(new Error('微信支付未能打开，请在微信中重新打开并继续原订单')),10000);document.addEventListener('WeixinJSBridgeReady',call,{once:true})}else call()})}
async function loadCoupons(){if(!couponField||!inWechat)return;const value=await requestJSON('/api/h5/coupons/available?target_ref='+encodeURIComponent('{{.Product.CouponTargetRef}}'),{credentials:'same-origin'});for(const item of value.items||[]){if(!Number.isSafeInteger(item.claim_id)||item.claim_id<1||typeof item.name!=='string'||!Number.isSafeInteger(item.discount_amount_minor)||item.discount_amount_minor<1||item.currency!=='CNY')continue;const option=document.createElement('option');option.value=String(item.claim_id);option.textContent=item.name+'（优惠 ¥'+(item.discount_amount_minor/100).toFixed(2)+'）';couponField.appendChild(option)}}
if(inWechat)void loadCoupons().catch(error=>{statusBox.textContent=error instanceof Error?error.message:'优惠券读取失败'});
async function currentCheckoutBinding(){const value=await requestJSON('/api/v1/wechat-pay/checkout-session',{credentials:'same-origin'}),binding=checkoutBinding(value.checkout_session_binding);if(!binding)throw requestFailure('unavailable','付款授权状态暂不可用');return binding}
const restartButton=document.getElementById('restart');restartButton.addEventListener('click',()=>{clearCheckout();restartButton.hidden=true;button.disabled=false;statusBox.textContent='如需再次购买，请点击购买'});async function restorePaidCheckout(){const checkpoint=readCheckout();if(!checkpoint||checkpoint.terminal_status!=='paid'||!checkpoint.merchant_order_no)return;button.disabled=true;restartButton.hidden=false;statusBox.textContent='正在恢复支付结果…';try{const value=await requestJSON('/api/v1/wechat-pay/checkouts/'+encodeURIComponent(checkpoint.merchant_order_no),{credentials:'same-origin'});if(value.status!=='paid')throw new Error('付款状态暂未确认，请使用原订单继续确认');statusBox.textContent='支付成功';showCompletionAction(value.completion_action);if(value.completion_action&&value.completion_action.state==='unavailable')statusBox.textContent='支付成功，后续指引暂不可用'}catch(error){statusBox.textContent=error instanceof Error?error.message:'支付结果暂不可用'}}if(inWechat)void restorePaidCheckout();
button.addEventListener('click',async()=>{button.disabled=true;let checkpoint=null;try{const beneficiary=document.getElementById('beneficiarySelf');if(!beneficiary.checked)throw new Error('请确认购买后权益归本人');checkpoint=readCheckout();if(!checkpoint){let mobile='';const field=document.getElementById('mobile');if(field){mobile=field.value.trim();if(!/^1[3-9][0-9]{9}$/.test(mobile))throw new Error('请输入正确的大陆 11 位手机号')}const payload={product_id:{{.Product.ID}},product_kind:'{{.Product.ProductKind}}',beneficiary_selection:'payer_self',coupon_claim_id:Number(couponField&&couponField.value)||0};if(mobile)payload.mobile='+86'+mobile;checkpoint=checkoutKey(payload,await currentCheckoutBinding());if(!checkpoint)throw new Error('无法保存本次订单恢复信息，请检查浏览器存储后重试')}if(checkpoint.legacy_unbound){if(!checkpoint.merchant_order_no)throw requestFailure('legacy_checkpoint_unbound','旧版订单恢复标识缺少付款会话绑定，已保留原标识，请勿重新下单');statusBox.textContent='正在恢复原订单…';await poll(checkpoint.merchant_order_no);return}if(checkpoint.merchant_order_no){statusBox.textContent='正在恢复原订单…';await poll(checkpoint.merchant_order_no);return}const currentBinding=await currentCheckoutBinding();if(currentBinding!==checkpoint.session_binding)throw requestFailure('session_mismatch','付款授权已变化，原订单标识已保留；请恢复原授权后继续');statusBox.textContent='正在恢复原订单…';const created=await requestJSON('/api/v1/wechat-pay/checkouts',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','Idempotency-Key':checkpoint.key},body:JSON.stringify({...checkpoint.payload,checkout_session_binding:checkpoint.session_binding})});checkpoint.merchant_order_no=created.merchant_order_no;if(!writeCheckout(checkpoint))throw new Error('无法保存原订单恢复信息，请勿重新下单');statusBox.textContent='等待微信支付…';await poll(created.merchant_order_no)}catch(error){statusBox.textContent=error instanceof Error?error.message:'支付结果尚未确认，请使用原订单重试';button.disabled=false}});{{end}}</script></body></html>`))
