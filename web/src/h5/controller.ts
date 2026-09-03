import { PageBase, type Vals } from '../shared/ui/runtime';
import { readPublicSurvey, readSurveyResult, submitSurvey } from '../api/public-survey';
import { toast } from '../shared/ui/feedback';

import type {
  PublicSurveyDefinition,
  PublicSurveyQuestion,
  PublicSurveyResult,
  PublicSurveySubmissionAnswer,
} from "../api/generated/health.schemas";

const validID = (value: number): boolean => Number.isSafeInteger(value) && value > 0;
const validToken = (value: string): boolean => /^[A-Za-z0-9_-]{43}$/.test(value);
const SUPPORTED_TYPES = ['single_choice', 'multi_choice', 'textarea', 'mobile'];

export class H5Controller extends PageBase {
  private definition: PublicSurveyDefinition | null = null;
  private result: PublicSurveyResult | null = null;
  private answers = new Map<number, number[]>();
  private textAnswers = new Map<number, string>();
  private unsupportedTypes = new Set<number>();
  private questionIndex = 0;
  private loading = false;
  private submitting = false;
  private error = '';
  private submissionKey = '';
  private resultToken = '';
  private submitted = false;

  constructor(readonly page: string) { super(); }

  async init(): Promise<void> {
    if (!['all', 'one', 'result'].includes(this.page)) return;
    this.loading = true;
    this.error = '';
    this.unsupportedTypes = new Set();
    this.refresh();
    try {
      if (this.page === 'result') {
        const query = new URLSearchParams(location.search);
        this.resultToken ||= new URLSearchParams((location.hash || '').slice(1)).get('result_token') || query.get('result_token') || '';
        if (!validToken(this.resultToken)) throw new Error('缺少有效结果凭据，无法查询提交结果');
        const result = await readSurveyResult(this.resultToken);
        if (!validID(result.submission_id) || !validID(result.definition_version) ||
            !Number.isFinite(Date.parse(result.submitted_at)) || result.local_only !== true || result.external_executed !== false) {
          throw new Error('提交结果响应不完整，未确认结果');
        }
        this.result = result;
      } else {
        const slug = new URLSearchParams(location.search).get('slug') || '';
        if (!/^[a-z0-9][a-z0-9-]{0,119}$/.test(slug)) throw new Error('缺少有效公开问卷 slug，不能填写或提交');
        const definition = await readPublicSurvey(slug);
        this.validateDefinition(definition, slug);
        this.definition = definition;
      }
    } catch (error) {
      this.definition = null;
      this.result = null;
      this.error = error instanceof Error ? error.message : '问卷读取失败，请重试';
    } finally {
      this.loading = false;
      this.refresh();
    }
  }

  private refresh(): void { this.__render?.(); }

  private validateDefinition(definition: PublicSurveyDefinition, slug: string): void {
    if (!definition || definition.slug !== slug || !validID(definition.id) || !validID(definition.version) ||
        typeof definition.title !== 'string' || !definition.title || typeof definition.description !== 'string' ||
        !['all_in_one', 'one_by_one'].includes(definition.answer_display_mode) ||
        !Array.isArray(definition.questions) || !definition.questions.length) throw new Error('公开问卷定义不完整');
    const ids = new Set<number>();
    this.unsupportedTypes = new Set();
    for (const question of definition.questions) {
      if (!validID(question.id) || ids.has(question.id) || typeof question.title !== 'string' || !question.title) {
        throw new Error('问卷题目响应不完整');
      }
      ids.add(question.id);
      // 契约外题型不整卷失败：降级为禁用题卡，由 renderVals 标注且不参与作答/提交。
      if (!SUPPORTED_TYPES.includes(question.type)) {
        this.unsupportedTypes.add(question.id);
        continue;
      }
      if (typeof question.required !== 'boolean' || !Array.isArray(question.options)) {
        throw new Error('问卷题目响应不完整');
      }
      if (['single_choice', 'multi_choice'].includes(question.type) && (!question.options.length ||
          !Number.isInteger(question.minimum_selections) || !Number.isInteger(question.maximum_selections) ||
          question.minimum_selections < 0 || question.maximum_selections < 1 ||
          question.minimum_selections > question.maximum_selections || question.maximum_selections > question.options.length ||
		  (question.required && question.minimum_selections === 0) || (question.type === 'single_choice' && question.maximum_selections !== 1))) {
        throw new Error('问卷题目超出当前单选/多选契约');
      }
	  if (['textarea', 'mobile'].includes(question.type) && question.options.length) throw new Error('输入题不应包含选项');
      const options = new Set<number>();
      for (const option of question.options) {
        if (!validID(option.id) || options.has(option.id) || typeof option.option_text !== 'string' || !option.option_text) throw new Error('问卷选项响应不完整');
        options.add(option.id);
      }
    }
  }

  private supported(question: PublicSurveyQuestion): boolean {
    return !this.unsupportedTypes.has(question.id);
  }

  private unsupportedNotice(): string {
    return this.definition && this.unsupportedTypes.size
      ? '本问卷包含当前端暂不支持的题型，相关题目已禁用且不参与提交；其余题目可正常作答'
      : '';
  }

  private select(question: PublicSurveyQuestion, optionID: number): void {
    if (this.submitting || this.submitted || !this.supported(question)) return;
    const previous = this.answers.get(question.id) || [];
    const next = question.type === 'single_choice' ? [optionID]
      : previous.includes(optionID) ? previous.filter((id) => id !== optionID) : [...previous, optionID];
    next.sort((a, b) => a - b);
    if (next.join(',') !== previous.join(',')) this.submissionKey = '';
    this.answers.set(question.id, next);
    this.error = '';
    this.refresh();
  }

  private setText(question: PublicSurveyQuestion, event: Event): void {
    if (this.submitting || this.submitted || !this.supported(question)) return;
    const value = String((event.target as HTMLInputElement | HTMLTextAreaElement).value || '');
    if (value !== (this.textAnswers.get(question.id) || '')) this.submissionKey = '';
    this.textAnswers.set(question.id, value);
    this.error = '';
  }

  private questionError(question: PublicSurveyQuestion): string {
    if (!this.supported(question)) return '';
    if (question.type === 'textarea' || question.type === 'mobile') {
      const value = this.textAnswers.get(question.id) || '';
      if (!value && !question.required) return '';
      if (!value) return `「${question.title}」请填写`;
      if (question.type === 'mobile' && !/^\+[1-9][0-9]{1,14}$/.test(value)) return `「${question.title}」请填写 E.164 手机号，例如 +8613800138000`;
      const length = [...value].length;
      if (length < (question.minimum_length || 0) || length > (question.maximum_length || 10000)) return `「${question.title}」长度不符合要求`;
      return '';
    }
    const count = (this.answers.get(question.id) || []).length;
    if (count === 0 && !question.required) return '';
    return count < question.minimum_selections || count > question.maximum_selections
      ? `「${question.title}」请选择 ${question.minimum_selections}–${question.maximum_selections} 项` : '';
  }

  private async submit(): Promise<void> {
    if (!this.definition || this.submitting || this.submitted) return;
    this.error = this.definition.questions.map((question) => this.questionError(question)).find(Boolean) || '';
    if (this.error) { this.refresh(); return; }
    const answers: PublicSurveySubmissionAnswer[] = this.definition.questions
      .filter((question) => this.supported(question) && ((this.answers.get(question.id) || []).length > 0 || (this.textAnswers.get(question.id) || '') !== ''))
      .map((question) => ({ question_id: question.id, option_ids: [...(this.answers.get(question.id) || [])], text_value: this.textAnswers.get(question.id) || undefined }));
    this.submitting = true;
    this.refresh();
    try {
      // One key per unchanged answer set in this filling lifecycle. Unknown
      // network outcomes retry the same request; editing answers clears the key.
      this.submissionKey ||= btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(32))))
        .replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
      const receipt = await submitSurvey(this.definition.slug, { version: this.definition.version, submission_key: this.submissionKey, answers });
      if (!validToken(receipt.resultToken)) throw new Error('提交回执缺少有效结果凭据，未确认结果');
      this.resultToken = receipt.resultToken;
      this.submitted = true;
    } catch (error) {
      this.error = error instanceof Error ? error.message : '提交失败；未修改答案时可安全重试';
    } finally {
      this.submitting = false;
      this.refresh();
    }
  }

  private move(delta: number): void {
    if (!this.definition || this.submitting || this.submitted) return;
    if (delta > 0) {
      this.error = this.questionError(this.definition.questions[this.questionIndex]);
      if (this.error) { this.refresh(); return; }
    }
    this.questionIndex = Math.max(0, Math.min(this.definition.questions.length - 1, this.questionIndex + delta));
    this.error = '';
    this.refresh();
  }

  private blocked(action: string): void {
    toast(`后端能力未就绪：${action}不可执行；未发起任何外部请求`, true);
  }

  /** 纯本地出口：回退上一页，无历史时回到 H5 屏幕列表；不发任何请求。 */
  private closePage(): void {
    if (window.history.length > 1) {
      window.history.back();
      return;
    }
    location.href = 'index.html';
  }

  renderVals(): Vals {
    const definition = this.definition;
    const stepMode = definition?.answer_display_mode === 'one_by_one';
    const questions = definition?.questions || [];
    const visible = stepMode ? questions.slice(this.questionIndex, this.questionIndex + 1) : questions;
    const ready = !!definition && !this.loading && !this.submitted;
    const current = visible[0];
    const errorText = this.error || this.unsupportedNotice();
    const assessment = (this.result?.assessment_result || {}) as Record<string, any>;
    const dimensions = Array.isArray(assessment.dimensions) ? assessment.dimensions : [];
    const legacyNext = (): void => {
      if (!definition || this.questionIndex < questions.length - 1) this.move(1);
      else void this.submit();
    };
    return {
      loading: this.loading, error: errorText, errorEmpty: !errorText, ready, result: this.result,
	  isAssessmentResult: this.result?.mode === 'assessment', resultTitle: this.result?.questionnaire_title || '问卷结果', totalScore: Number(this.result?.total_score || 0), overallTitle: assessment.overall_level?.title || '已完成', overallSummary: assessment.overall_level?.summary || '', dimensions,
      resultTime: this.result ? new Date(this.result.submitted_at).toLocaleString('zh-CN', { hour12: false }) : '',
      notWechatUA: !/MicroMessenger/i.test(navigator.userAgent || ''),
      submitted: this.submitted, resultPath: `result.html#result_token=${encodeURIComponent(this.resultToken)}`,
      title: definition?.title || '公开问卷', description: definition?.description || '',
      progress: stepMode ? `第 ${this.questionIndex + 1} / ${questions.length} 题` : `共 ${questions.length} 题`,
      canPrevious: ready && stepMode && this.questionIndex > 0 && !this.submitting,
      canNext: ready && stepMode && this.questionIndex < questions.length - 1 && !this.submitting,
      canSubmit: ready && (!stepMode || this.questionIndex === questions.length - 1) && !this.submitting,
      submitting: this.submitting,
      canRetry: !this.loading && !!this.error && !definition,
      blockedReason: this.page === 'auth'
        ? '后端能力未就绪：H5 OAuth Provider 当前禁用，不能授权。请使用已发布的匿名问卷测试入口。'
        : '后端能力未就绪：当前页面没有可用的报名、支付、续费、二维码或完成状态契约；未执行任何外部操作。',
      questions: visible.map((question) => {
        const supported = this.supported(question);
        const isText = question.type === 'textarea' || question.type === 'mobile';
        return {
          id: question.id, title: question.title, required: question.required ? '（必答）' : '（选答）',
          supported, unsupported: !supported,
          isText, isChoice: !isText, inputType: question.type === 'mobile' ? 'tel' : 'text', inputValue: this.textAnswers.get(question.id) || '', placeholder: question.placeholder_text || '请输入', input: (event: Event) => this.setText(question, event),
          hint: !supported ? '当前端不支持该题型，本题不参与作答与提交'
            : question.type === 'single_choice' ? '单选' : question.type === 'multi_choice' ? `多选，限选 ${question.maximum_selections} 项` : question.type === 'mobile' ? '手机号（国际格式）' : '文本回答',
          options: supported && !isText ? question.options.map((option) => {
            const selected = (this.answers.get(question.id) || []).includes(option.id);
            return { id: option.id, text: option.option_text, selected, mark: selected ? '✓' : '○',
              style: { background: selected ? '#EFF4FF' : '#fff', borderColor: selected ? '#3370ff' : '#DEE0E3' },
              pick: () => this.select(question, option.id) };
          }) : [],
        };
      }),
      qProgressText: stepMode && questions.length ? `第 ${this.questionIndex + 1} / ${questions.length} 题` : '尚未读取题目',
      qPct: stepMode && questions.length ? Math.round(((this.questionIndex + 1) / questions.length) * 100) : 0,
      qTitle: current?.title || '问卷题目',
      act: {
        submit: () => { void this.submit(); }, previous: () => this.move(-1), next: () => this.move(1), retry: () => { void this.init(); },
        authContinue: () => this.blocked('H5 微信授权'), submitAll: () => { void this.submit(); }, prevQ: () => this.move(-1), nextQ: legacyNext,
        signup: () => this.blocked('报名'), pay: () => this.blocked('支付'), renew: () => this.blocked('续费'),
        addWx: () => this.blocked('添加企微账号'), close: () => this.closePage(),
      },
    };
  }
}
