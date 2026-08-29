/* ============================================================
   AI Agent — Vue 3 SPA (CDN mode, no build step)
   BUILD_TAG: ds-rd13 (2026-08-20) — fix: fmtTime 未导出到 ChatPage setup return,模板调用报 TypeError
   ============================================================ */

console.log('%c[app.js] build tag: ds-rd11', 'background:#3B82F6;color:#fff;padding:2px 8px;font-weight:bold;border-radius:3px');

const { createApp, ref, reactive, computed, onMounted, onUnmounted, nextTick, watch, h } = Vue;
const { createRouter, createWebHistory, useRouter, useRoute } = VueRouter;

/* ---------- Constants ---------- */
const API_BASE = '/api/v1';

/* ---------- Auth utilities ---------- */
function getToken() {
  return localStorage.getItem('token') || '';
}

let _redirecting = false;
async function authFetch(url, opts = {}) {
  const token = getToken();
  opts.headers = {
    'Authorization': 'Bearer ' + token,
    'Content-Type': 'application/json',
    ...(opts.headers || {})
  };
  const r = await fetch(url, opts);
  if (r.status === 401) {
    if (!_redirecting) {
      _redirecting = true;
      localStorage.removeItem('token');
      localStorage.removeItem('user_id');
      localStorage.removeItem('email');
      window.location.replace('/login');
    }
    throw new Error('unauthorized');
  }
  return r;
}

/* ---------- Formatting utilities ---------- */
function escapeHtml(s) {
  const el = document.createElement('span');
  el.textContent = s == null ? '' : String(s);
  return el.innerHTML;
}

function fmtDate(s) {
  if (!s) return '-';
  const d = new Date(s);
  if (isNaN(d.getTime())) return '-';
  const time = d.toLocaleTimeString();
  return d.toLocaleDateString() + ' ' + time.slice(0, 5);
}

/* ---------- Lucide icons helper ----------
   Tailwind 重渲染会把 <i data-lucide="..."> 节点替换成新节点,
   老节点上的 lucide svg 就丢了。每次 messages / sessions 变化后
   都要重新跑一次 createIcons()。 */
function loadIcons() {
  try {
    if (window.lucide && typeof window.lucide.createIcons === 'function') {
      window.lucide.createIcons();
    }
  } catch (e) {
    // 不阻塞主流程
  }
}

/* 根据 agent 标题返回 lucide icon 名(前端规则,后端如果有 icon 字段优先用) */
function agentIconByTitle(title) {
  const t = (title || '').toLowerCase();
  if (/代码|编程|dev|code|程序员|engineer/.test(t)) return 'code-2';
  if (/翻译|translate/.test(t)) return 'languages';
  if (/写作|文案|创作|writer|copy/.test(t)) return 'pen-tool';
  if (/搜索|search|检索/.test(t)) return 'search';
  if (/分析|analyst|数据|data/.test(t)) return 'bar-chart-3';
  if (/客户|客服|support|cs/.test(t)) return 'headphones';
  if (/老师|teacher|教学|教育|tutor/.test(t)) return 'graduation-cap';
  if (/医生|doctor|医疗|health/.test(t)) return 'heart-pulse';
  if (/财务|finance|会计|account/.test(t)) return 'calculator';
  if (/设计|design/.test(t)) return 'palette';
  if (/助理|助手|helper|assistant/.test(t)) return 'sparkles';
  return 'bot';
}

/* 复制消息内容到剪贴板,失败给个吐司 */
async function copyToClipboard(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
    } else {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
    }
    if (typeof window.showToast === 'function') window.showToast('已复制');
  } catch (e) {
    if (typeof window.showToast === 'function') window.showToast('复制失败');
  }
}

/* 短时间格式:HH:MM(本地时区) */
function fmtTime(s) {
  if (!s) return '';
  const d = new Date(s);
  if (isNaN(d.getTime())) return '';
  return d.toTimeString().slice(0, 5);
}

function renderMarkdown(text) {
  if (!text) return '';
  if (typeof marked === 'undefined') return escapeHtml(text);
  try {
    const cleaned = String(text).replace(/\n{3,}/g, '\n\n');
    return marked.parse(cleaned);
  } catch (e) {
    // 兜底:marked.parse 抛错时降级到 escapeHtml,保证 DOM 仍然有内容
    console.error('[renderMarkdown] parse failed, fallback to escapeHtml', e);
    return escapeHtml(String(text));
  }
}

/* ---------- Global Toast ---------- */
const toastState = reactive({ show: false, msg: '' });
let _toastTimer = null;
window.showToast = function (msg) {
  toastState.msg = msg;
  toastState.show = true;
  if (_toastTimer) clearTimeout(_toastTimer);
  _toastTimer = setTimeout(() => { toastState.show = false; }, 2000);
};

/* ---------- Auto-resize textarea helper ---------- */
function autoResize(el, max) {
  max = max || 200;
  el.style.height = 'auto';
  el.style.height = Math.min(el.scrollHeight, max) + 'px';
}

/* ---------- Configure marked ---------- */
if (typeof marked !== 'undefined') {
  marked.setOptions({ breaks: false, gfm: true });
}

/* ============================================================
   LoginPage
   ============================================================ */
const LoginPage = {
  template: `
    <div class="min-h-screen w-full flex bg-background text-foreground overflow-hidden">

      <!-- 左半屏:品牌展示 -->
      <div class="hidden md:flex md:w-1/2 lg:w-3/5 bg-card border-r border-border flex-col justify-between p-10 lg:p-14 relative overflow-hidden">

        <!-- 装饰网格背景 -->
        <div
          class="absolute inset-0 pointer-events-none opacity-50"
          style="background-image: radial-gradient(circle at 1px 1px, rgba(59,130,246,0.10) 1px, transparent 0); background-size: 28px 28px;"
        ></div>
        <!-- 装饰圆形 -->
        <div class="absolute -top-32 -left-32 w-96 h-96 rounded-full pointer-events-none" style="background: radial-gradient(circle, rgba(59,130,246,0.10), transparent 70%);"></div>
        <div class="absolute bottom-0 right-0 w-80 h-80 rounded-full pointer-events-none" style="background: radial-gradient(circle, rgba(59,130,246,0.06), transparent 70%);"></div>

        <!-- Logo -->
        <div class="relative z-10 flex items-center gap-3">
          <div class="h-10 w-10 rounded-xl bg-primary flex items-center justify-center ai-avatar-ring">
            <i data-lucide="bot" class="h-5 w-5 text-primary-foreground ai-float"></i>
          </div>
          <div class="leading-tight">
            <div class="font-semibold text-base tracking-tight">ansmeee</div>
            <div class="text-xs text-muted-foreground">Agent Platform</div>
          </div>
        </div>

        <!-- 中央文案 + 特性 -->
        <div class="relative z-10 max-w-md">
          <div class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-primary/10 text-primary text-[11px] font-medium mb-5">
            <span class="w-1.5 h-1.5 rounded-full bg-primary ai-pulse"></span>
            AI Agent Workspace
          </div>
          <h1 class="text-3xl lg:text-4xl font-semibold text-foreground mb-3 tracking-tight leading-tight">
            下一代<br>AI Agent 工作站
          </h1>
          <p class="text-sm text-muted-foreground mb-8 leading-relaxed">
            基于 LangChainGo + Gin 构建,为你的业务提供可观测、可定制、可扩展的智能体能力。
          </p>

          <div class="space-y-3">
            <div class="flex items-start gap-3 group">
              <div class="h-9 w-9 rounded-lg bg-primary/10 text-primary flex items-center justify-center flex-shrink-0 group-hover:bg-primary/20 transition">
                <i data-lucide="zap" class="h-4 w-4"></i>
              </div>
              <div class="pt-0.5">
                <div class="text-sm font-medium text-foreground">ReAct 推理循环</div>
                <div class="text-xs text-muted-foreground mt-0.5">思考 → 行动 → 观察,多步推理可见可控</div>
              </div>
            </div>
            <div class="flex items-start gap-3 group">
              <div class="h-9 w-9 rounded-lg bg-primary/10 text-primary flex items-center justify-center flex-shrink-0 group-hover:bg-primary/20 transition">
                <i data-lucide="brain" class="h-4 w-4"></i>
              </div>
              <div class="pt-0.5">
                <div class="text-sm font-medium text-foreground">三层记忆架构 L0-L2</div>
                <div class="text-xs text-muted-foreground mt-0.5">短期会话 / 滚动摘要 / 长期画像,跨会话连贯</div>
              </div>
            </div>
            <div class="flex items-start gap-3 group">
              <div class="h-9 w-9 rounded-lg bg-primary/10 text-primary flex items-center justify-center flex-shrink-0 group-hover:bg-primary/20 transition">
                <i data-lucide="book-open" class="h-4 w-4"></i>
              </div>
              <div class="pt-0.5">
                <div class="text-sm font-medium text-foreground">知识库 RAG 问答</div>
                <div class="text-xs text-muted-foreground mt-0.5">每个 Agent 独立知识库,文档切片 + 语义检索</div>
              </div>
            </div>
            <div class="flex items-start gap-3 group">
              <div class="h-9 w-9 rounded-lg bg-primary/10 text-primary flex items-center justify-center flex-shrink-0 group-hover:bg-primary/20 transition">
                <i data-lucide="wrench" class="h-4 w-4"></i>
              </div>
              <div class="pt-0.5">
                <div class="text-sm font-medium text-foreground">多工具 &amp; 多模型</div>
                <div class="text-xs text-muted-foreground mt-0.5">自定义 Tool,OpenAI 兼容协议多模型可切换</div>
              </div>
            </div>
          </div>
        </div>

        <!-- 底部版本号 -->
        <div class="relative z-10 flex items-center gap-2 text-xs text-muted-foreground">
          <i data-lucide="github" class="h-3.5 w-3.5"></i>
          <span>v1.0 · Aurora Glass</span>
          <span class="mx-1">·</span>
          <i data-lucide="shield-check" class="h-3.5 w-3.5"></i>
          <span>JWT Auth · End-to-End Encrypted</span>
        </div>

        <!-- 装饰浮动小卡片 -->
        <div class="absolute top-32 right-12 ai-float hidden xl:block" style="animation-delay: -1s;">
          <div class="bg-card border border-border rounded-xl px-3 py-2 shadow-lg flex items-center gap-2">
            <i data-lucide="message-circle" class="h-3.5 w-3.5 text-primary"></i>
            <span class="text-xs font-medium text-foreground">多轮对话</span>
          </div>
        </div>
        <div class="absolute bottom-40 right-20 ai-float hidden xl:block" style="animation-delay: -3s;">
          <div class="bg-card border border-border rounded-xl px-3 py-2 shadow-lg flex items-center gap-2">
            <i data-lucide="database" class="h-3.5 w-3.5 text-primary"></i>
            <span class="text-xs font-medium text-foreground">RAG 检索</span>
          </div>
        </div>
      </div>

      <!-- 右半屏:登录表单 -->
      <div class="flex-1 flex flex-col items-center justify-center p-6 sm:p-10 bg-background relative">
        <!-- 移动端 logo -->
        <div class="md:hidden flex items-center gap-2 mb-8 self-start">
          <div class="h-9 w-9 rounded-lg bg-primary flex items-center justify-center">
            <i data-lucide="bot" class="h-5 w-5 text-primary-foreground"></i>
          </div>
          <span class="font-semibold tracking-tight">ansmeee</span>
        </div>

        <div class="w-full max-w-sm">
          <div class="mb-8">
            <h2 class="text-2xl font-semibold text-foreground mb-1.5 tracking-tight" data-dom-id="login-title">
              {{ mode === 'login' ? '欢迎回来 👋' : '创建账号' }}
            </h2>
            <p class="text-sm text-muted-foreground">
              {{ mode === 'login' ? '登录继续你的 AI Agent 之旅' : '几秒钟即可开始使用' }}
            </p>
          </div>

          <!-- 错误提示 -->
          <div
            v-if="error"
            class="mb-4 flex items-start gap-2 px-3 py-2.5 rounded-lg border border-destructive/30 bg-destructive/10 text-destructive text-sm ai-card-shadow"
            data-dom-id="login-error"
          >
            <i data-lucide="alert-circle" class="h-4 w-4 flex-shrink-0 mt-0.5"></i>
            <span class="flex-1">{{ error }}</span>
            <button class="opacity-60 hover:opacity-100" @click="error = ''" title="关闭">
              <i data-lucide="x" class="h-3.5 w-3.5"></i>
            </button>
          </div>

          <!-- 表单 -->
          <div class="space-y-4">
            <div>
              <label class="block text-sm font-medium text-foreground mb-1.5">邮箱</label>
              <div class="relative">
                <i data-lucide="mail" class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none"></i>
                <input
                  type="email"
                  v-model="email"
                  placeholder="your@email.com"
                  @keyup.enter="submit"
                  autocomplete="email"
                  class="w-full pl-10 pr-4 py-2.5 rounded-lg border border-border bg-card text-sm text-foreground placeholder:text-muted-foreground/70 outline-none transition focus:border-primary/50 focus:ring-2 focus:ring-primary/20"
                  data-dom-id="login-email"
                >
              </div>
            </div>

            <div>
              <label class="block text-sm font-medium text-foreground mb-1.5">密码</label>
              <div class="relative">
                <i data-lucide="lock" class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none"></i>
                <input
                  type="password"
                  v-model="password"
                  placeholder="至少 6 位"
                  @keyup.enter="submit"
                  autocomplete="current-password"
                  class="w-full pl-10 pr-4 py-2.5 rounded-lg border border-border bg-card text-sm text-foreground placeholder:text-muted-foreground/70 outline-none transition focus:border-primary/50 focus:ring-2 focus:ring-primary/20"
                  data-dom-id="login-password"
                >
              </div>
            </div>

            <button
              class="w-full flex items-center justify-center gap-2 px-4 py-2.5 rounded-lg bg-primary text-primary-foreground text-sm font-semibold hover:bg-primary/90 active:scale-[0.98] ai-glow transition-all duration-150 disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:scale-100"
              @click="submit"
              :disabled="loading"
              data-dom-id="login-submit"
            >
              <i v-if="loading" data-lucide="loader-2" class="h-4 w-4 animate-spin"></i>
              <span>{{ loading ? '处理中…' : (mode === 'login' ? '登录' : '注册') }}</span>
              <i v-if="!loading" data-lucide="arrow-right" class="h-4 w-4"></i>
            </button>
          </div>

          <!-- 切换登录/注册 -->
          <div class="mt-6 text-center text-sm text-muted-foreground">
            {{ mode === 'login' ? '没有账号?' : '已经有账号了?' }}
            <button
              class="ml-1 text-primary font-medium hover:underline transition"
              @click="toggleMode"
              data-dom-id="login-toggle"
            >
              {{ mode === 'login' ? '立即注册' : '返回登录' }}
            </button>
          </div>

          <!-- 分隔线 + 装饰 -->
          <div class="mt-8 pt-6 border-t border-border flex items-center justify-center gap-2 text-[11px] text-muted-foreground">
            <i data-lucide="shield" class="h-3 w-3"></i>
            <span>登录即表示你同意我们的服务条款与隐私政策</span>
          </div>
        </div>
      </div>
    </div>
  `,
  setup() {
    const router = useRouter();
    const email = ref('');
    const password = ref('');
    const mode = ref('login');
    const error = ref('');
    const loading = ref(false);

    function toggleMode() {
      mode.value = mode.value === 'login' ? 'register' : 'login';
      error.value = '';
    }

    async function submit() {
      const em = email.value.trim();
      const pw = password.value.trim();
      if (!em || !pw) { error.value = '请填写邮箱和密码'; return; }
      if (pw.length < 6) { error.value = '密码至少 6 位'; return; }
      if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(em)) { error.value = '邮箱格式不正确'; return; }

      loading.value = true;
      error.value = '';
      try {
        const url = API_BASE + (mode.value === 'login' ? '/auth/login' : '/auth/register');
        const r = await fetch(url, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ email: em, password: pw })
        });
        const d = await r.json();
        if (d.code === 0 && d.data && d.data.token) {
          localStorage.setItem('token', d.data.token);
          localStorage.setItem('user_id', d.data.user_id || '');
          localStorage.setItem('email', d.data.email || '');
          router.replace('/');
        } else {
          error.value = d.message || '操作失败';
        }
      } catch (e) {
        error.value = '网络错误,请重试';
      } finally {
        loading.value = false;
      }
    }

    onMounted(async () => {
      // lucide 图标初始化(必须在 token 检查之前,否则邮箱/密码 input 里的
      // <i data-lucide="mail/lock"> 会渲染成空 <i>)
      nextTick(() => loadIcons());

      const token = getToken();
      if (!token) return;
      try {
        const r = await fetch(API_BASE + '/auth/me', {
          headers: { 'Authorization': 'Bearer ' + token }
        });
        if (r.ok) {
          router.replace('/');
        } else {
          localStorage.removeItem('token');
          localStorage.removeItem('user_id');
          localStorage.removeItem('email');
        }
      } catch (e) { /* stay on login */ }
    });

    return { email, password, mode, error, loading, toggleMode, submit };
  }
};

/* ============================================================
   AgentsPage
   ============================================================ */
const AgentsPage = {
  template: `
    <div class="mx-auto w-full max-w-6xl px-4 pb-12 pt-6 sm:px-6 sm:pt-8">

      <!-- 页面标题 + 统计徽章 -->
      <header class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 class="text-2xl font-semibold tracking-tight text-foreground">智能体管理</h1>
          <p class="mt-1 text-sm text-muted-foreground">创建、管理你的 AI 智能体,并一键发起对话</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <span class="inline-flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1 text-xs text-muted-foreground">
            <i data-lucide="bot" class="h-3.5 w-3.5 text-primary"></i>
            智能体 {{ agents.length }}
          </span>
          <span class="inline-flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1 text-xs text-muted-foreground">
            <i data-lucide="wrench" class="h-3.5 w-3.5 text-primary"></i>
            工具 {{ totalTools }}
          </span>
          <span class="inline-flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1 text-xs text-muted-foreground">
            <i data-lucide="book-open" class="h-3.5 w-3.5 text-primary"></i>
            文档 {{ totalKB }}
          </span>
        </div>
      </header>

      <!-- Hero 快速提问 -->
      <section class="mb-8 rounded-xl border border-border bg-card p-5 shadow-sm sm:mb-10 sm:p-6">
        <div class="mb-3 flex items-center gap-2 text-sm text-muted-foreground">
          <i data-lucide="sparkles" class="h-4 w-4 text-primary"></i>
          <span>向 <strong class="font-semibold text-foreground">{{ defaultAgentName || '默认助手' }}</strong> 提问</span>
        </div>
        <div class="relative">
          <textarea
            v-model="quickInput"
            rows="3"
            placeholder="输入消息，Enter 发送，Shift+Enter 换行"
            @input="onQuickInput"
            @keydown="onQuickKey"
            :disabled="!defaultAgentId"
            class="min-h-[100px] w-full resize-y rounded-lg border border-input bg-muted px-4 py-3 pr-14 text-sm leading-relaxed text-foreground outline-none transition-all placeholder:text-muted-foreground focus:border-primary focus:ring-2 focus:ring-ring/30 disabled:opacity-50"
          ></textarea>
          <button
            @click="quickSend"
            :disabled="!quickInput.trim() || !defaultAgentId"
            title="发送"
            class="absolute bottom-3 right-3 inline-flex h-9 w-9 items-center justify-center rounded-md bg-primary text-primary-foreground shadow-sm transition-all hover:bg-primary/90 active:scale-95 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <i data-lucide="send" class="h-4 w-4"></i>
          </button>
        </div>
        <p v-if="!defaultAgentId && !loading" class="mt-3 text-xs text-muted-foreground">
          还没有可用的智能体,请先
          <button @click="openCreate" class="font-medium text-primary underline-offset-2 hover:underline">创建一个</button>
        </p>
      </section>

      <!-- 智能体列表 -->
      <section>
        <div class="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <h2 class="text-base font-semibold text-foreground">我的智能体</h2>
          <div class="flex items-center gap-3">
            <div class="relative">
              <i data-lucide="search" class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"></i>
              <input
                v-model="searchKw"
                type="text"
                placeholder="搜索智能体..."
                class="h-9 w-full rounded-md border border-input bg-card pl-9 pr-3 text-sm text-foreground outline-none transition-all placeholder:text-muted-foreground focus:border-primary focus:ring-2 focus:ring-ring/30 sm:w-56"
              >
            </div>
            <button
              @click="openCreate"
              class="inline-flex h-9 items-center justify-center gap-1.5 whitespace-nowrap rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground shadow-sm transition-all hover:bg-primary/90 active:scale-95"
            >
              <i data-lucide="plus" class="h-4 w-4"></i>
              新建
            </button>
          </div>
        </div>

        <!-- 加载骨架屏 -->
        <div v-if="loading" class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div v-for="n in 6" :key="'skel-' + n" class="rounded-xl border border-border bg-card p-5 shadow-sm">
            <div class="mb-3 flex items-start justify-between gap-3">
              <div class="h-5 w-32 animate-pulse rounded bg-muted"></div>
              <div class="h-6 w-6 animate-pulse rounded bg-muted"></div>
            </div>
            <div class="mb-2 h-3 w-full animate-pulse rounded bg-muted"></div>
            <div class="mb-4 h-3 w-2/3 animate-pulse rounded bg-muted"></div>
            <div class="flex gap-2">
              <div class="h-5 w-14 animate-pulse rounded-md bg-muted"></div>
              <div class="h-5 w-14 animate-pulse rounded-md bg-muted"></div>
              <div class="h-5 w-14 animate-pulse rounded-md bg-muted"></div>
            </div>
          </div>
        </div>

        <!-- 卡片网格 -->
        <div v-else-if="filteredAgents.length > 0" class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <article
            v-for="a in filteredAgents"
            :key="a.id"
            @click="startChat(a.id)"
            class="group relative flex cursor-pointer flex-col rounded-xl border border-border bg-card p-5 shadow-sm transition-all hover:-translate-y-0.5 hover:border-primary/30 hover:shadow-md"
          >
            <div class="mb-2 flex items-start justify-between gap-3">
              <div class="flex min-w-0 flex-1 items-start gap-3">
                <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary transition-colors group-hover:bg-primary group-hover:text-primary-foreground">
                  <i :data-lucide="agentIconByTitle(a.title)" class="h-5 w-5"></i>
                </div>
                <div class="min-w-0 flex-1">
                  <h3 class="line-clamp-1 text-base font-semibold text-foreground">{{ a.title }}</h3>
                  <p class="mt-0.5 font-mono text-[11px] text-muted-foreground">ID: {{ a.id.slice(0, 8) }}</p>
                </div>
              </div>
              <div class="relative shrink-0">
                <button
                  @click.stop="toggleMenu(a.id)"
                  class="flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                  title="操作"
                >
                  <i data-lucide="more-vertical" class="h-4 w-4"></i>
                </button>
                <div v-if="openMenu === a.id" class="absolute right-0 top-9 z-10 min-w-[140px] overflow-hidden rounded-lg border border-border bg-popover p-1 shadow-lg">
                  <button
                    @click.stop="editAgent(a.id)"
                    class="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm text-popover-foreground transition-colors hover:bg-muted"
                  >
                    <i data-lucide="pencil" class="h-4 w-4 text-muted-foreground"></i>
                    编辑配置
                  </button>
                  <button
                    @click.stop="deleteAgent(a)"
                    class="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm text-destructive transition-colors hover:bg-destructive/10"
                  >
                    <i data-lucide="trash-2" class="h-4 w-4"></i>
                    删除
                  </button>
                </div>
              </div>
            </div>
            <p class="mb-4 line-clamp-2 flex-1 text-sm leading-relaxed text-muted-foreground">
              {{ a.description || '暂无描述' }}
            </p>
            <div class="mb-3 flex flex-wrap gap-1.5">
              <span v-if="a.model_cfg && a.model_cfg.model" class="inline-flex items-center gap-1 rounded-md border border-border bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
                <i data-lucide="cpu" class="h-3 w-3"></i>
                {{ a.model_cfg.model }}
              </span>
              <span class="inline-flex items-center gap-1 rounded-md border border-border bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
                <i data-lucide="wrench" class="h-3 w-3"></i>
                {{ (a.tools && a.tools.length) || 0 }} 工具
              </span>
              <span class="inline-flex items-center gap-1 rounded-md border border-border bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
                <i data-lucide="book-open" class="h-3 w-3"></i>
                {{ (a.kb_cfg && a.kb_cfg.docs_count) || 0 }} 文档
              </span>
            </div>
            <div class="flex items-center justify-between border-t border-border pt-3 text-xs text-muted-foreground">
              <span class="inline-flex items-center gap-1">
                <i data-lucide="clock" class="h-3 w-3"></i>
                {{ fmtDate(a.updated_at) }}
              </span>
              <span class="inline-flex items-center gap-1 text-primary opacity-0 transition-opacity group-hover:opacity-100">
                进入对话
                <i data-lucide="arrow-right" class="h-3 w-3"></i>
              </span>
            </div>
          </article>
        </div>

        <!-- 空状态 -->
        <div v-else class="flex flex-col items-center justify-center rounded-xl border border-dashed border-border bg-card py-16 text-center">
          <div class="mb-4 flex h-14 w-14 items-center justify-center rounded-full bg-muted text-muted-foreground">
            <i data-lucide="bot" class="h-6 w-6"></i>
          </div>
          <p class="text-sm font-medium text-foreground">
            {{ searchKw ? '未找到匹配的智能体' : '还没有智能体' }}
          </p>
          <p class="mt-1 text-xs text-muted-foreground">
            {{ searchKw ? '试试其他关键词' : '点击右上角"新建"按钮创建你的第一个智能体' }}
          </p>
          <button
            v-if="!searchKw"
            @click="openCreate"
            class="mt-4 inline-flex h-9 items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground shadow-sm transition-all hover:bg-primary/90 active:scale-95"
          >
            <i data-lucide="plus" class="h-4 w-4"></i>
            立即创建
          </button>
        </div>
      </section>

      <!-- 创建 / 编辑 模态框 -->
      <div v-if="showModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm" @click.self="closeModal">
        <div class="w-full max-w-lg rounded-xl border border-border bg-card p-6 shadow-xl" role="dialog" aria-modal="true">
          <div class="mb-5 flex items-center justify-between">
            <h3 class="text-lg font-semibold text-foreground">新增智能体</h3>
            <button @click="closeModal" class="flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground" title="关闭">
              <i data-lucide="x" class="h-4 w-4"></i>
            </button>
          </div>
          <div class="space-y-4">
            <label class="block text-sm font-medium text-foreground">
              名称
              <div class="relative mt-1.5">
                <i data-lucide="bot" class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"></i>
                <input
                  v-model="form.title"
                  maxlength="50"
                  placeholder="Agent 名称"
                  class="block h-10 w-full rounded-md border border-input bg-muted pl-9 pr-3 text-sm text-foreground outline-none transition-all placeholder:text-muted-foreground focus:border-primary focus:ring-2 focus:ring-ring/30"
                >
              </div>
            </label>
            <label class="block text-sm font-medium text-foreground">
              描述
              <div class="relative mt-1.5">
                <i data-lucide="file-text" class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"></i>
                <input
                  v-model="form.description"
                  maxlength="200"
                  placeholder="简短描述此 Agent 的功能"
                  class="block h-10 w-full rounded-md border border-input bg-muted pl-9 pr-3 text-sm text-foreground outline-none transition-all placeholder:text-muted-foreground focus:border-primary focus:ring-2 focus:ring-ring/30"
                >
              </div>
            </label>
            <label class="block text-sm font-medium text-foreground">
              系统提示词 (Prompt)
              <textarea
                v-model="form.prompt"
                rows="5"
                placeholder="定义 Agent 的行为和角色"
                class="mt-1.5 block w-full resize-y rounded-md border border-input bg-muted px-3 py-2.5 text-sm leading-relaxed text-foreground outline-none transition-all placeholder:text-muted-foreground focus:border-primary focus:ring-2 focus:ring-ring/30"
              ></textarea>
            </label>
          </div>
          <div class="mt-6 flex justify-end gap-3">
            <button
              @click="closeModal"
              class="inline-flex h-9 items-center justify-center rounded-md border border-border bg-transparent px-4 text-sm font-medium text-foreground transition-colors hover:bg-muted"
            >
              取消
            </button>
            <button
              @click="saveAgent"
              :disabled="saving"
              class="inline-flex h-9 items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground shadow-sm transition-all hover:bg-primary/90 active:scale-95 disabled:opacity-50"
            >
              <i v-if="saving" data-lucide="loader-2" class="h-4 w-4 animate-spin"></i>
              {{ saving ? '保存中...' : '保存' }}
            </button>
          </div>
        </div>
      </div>
    </div>
  `,
  setup() {
    const router = useRouter();
    const agents = ref([]);
    const searchKw = ref('');
    const showModal = ref(false);
    const saving = ref(false);
    const openMenu = ref(null);
    const form = reactive({ title: '', description: '', prompt: '' });
    const loading = ref(true);
    const quickInput = ref('');

    const defaultAgentId = computed(() => agents.value.length > 0 ? agents.value[0].id : '');
    const defaultAgentName = computed(() => agents.value.length > 0 ? agents.value[0].title : '');

    const filteredAgents = computed(() => {
      const kw = searchKw.value.trim().toLowerCase();
      if (!kw) return agents.value;
      return agents.value.filter(a =>
        (a.title || '').toLowerCase().includes(kw) ||
        (a.description || '').toLowerCase().includes(kw)
      );
    });

    const totalTools = computed(() => agents.value.reduce((s, a) => s + ((a.tools && a.tools.length) || 0), 0));
    const totalKB = computed(() => agents.value.reduce((s, a) => s + ((a.kb_cfg && a.kb_cfg.docs_count) || 0), 0));

    async function loadAgents() {
      loading.value = true;
      try {
        const r = await authFetch(API_BASE + '/agents');
        const d = await r.json();
        agents.value = (d.data && d.data.agents) || [];
      } catch (e) {}
      finally {
        loading.value = false;
        nextTick(() => loadIcons());
      }
    }

    function onQuickInput(e) {
      const el = e.target;
      el.style.height = 'auto';
      el.style.height = Math.min(el.scrollHeight, 200) + 'px';
    }

    function onQuickKey(e) {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        quickSend();
      }
    }

    function quickSend() {
      const msg = quickInput.value.trim();
      if (!msg || !defaultAgentId.value) return;
      // 长消息走 URL 会有截断风险(浏览器对 query 长度有限制),改存 sessionStorage
      try {
        sessionStorage.setItem('__pending_msg', msg);
      } catch (e) {}
      router.push('/chat?agent_id=' + defaultAgentId.value);
    }

    function toggleMenu(id) {
      openMenu.value = openMenu.value === id ? null : id;
    }

    function startChat(id) {
      router.push('/chat?agent_id=' + id);
    }

    function editAgent(id) {
      openMenu.value = null;
      router.push('/agent?id=' + id);
    }

    async function deleteAgent(a) {
      openMenu.value = null;
      if (!confirm('确认删除 "' + (a.title || '') + '"？删除后不可恢复。')) return;
      try {
        const r = await authFetch(API_BASE + '/agents/' + a.id, { method: 'DELETE' });
        const d = await r.json();
        if (d.code === 0) {
          showToast('已删除');
          await loadAgents();
        } else {
          showToast(d.message || '删除失败');
        }
      } catch (e) {}
    }

    function openCreate() {
      form.title = '';
      form.description = '';
      form.prompt = '';
      showModal.value = true;
    }

    function closeModal() {
      showModal.value = false;
    }

    async function saveAgent() {
      const title = form.title.trim();
      const prompt = form.prompt.trim();
      if (!title) { showToast('请填写名称'); return; }
      if (!prompt) { showToast('请填写系统提示词'); return; }
      saving.value = true;
      try {
        const r = await authFetch(API_BASE + '/agents', {
          method: 'POST',
          body: JSON.stringify({ title, description: form.description.trim(), prompt })
        });
        const d = await r.json();
        if (d.code === 0) {
          showModal.value = false;
          showToast('创建成功');
          await loadAgents();
        } else {
          showToast(d.message || '创建失败');
        }
      } catch (e) {
        showToast('网络错误');
      } finally {
        saving.value = false;
      }
    }

    function onDocClick(e) {
      // 菜单按钮自己有 @click.stop 拦截冒泡,这里只关掉其他位置触发的菜单
      if (e.target && e.target.closest && e.target.closest('[data-dom-id="agents-card-menu"]')) return;
      openMenu.value = null;
    }

    onMounted(async () => {
      await loadAgents();
      document.addEventListener('click', onDocClick);
    });
    onUnmounted(() => {
      document.removeEventListener('click', onDocClick);
    });

    // 任何状态变化触发 lucide 图标重新渲染
    watch([agents, openMenu, showModal, searchKw], () => {
      nextTick(() => loadIcons());
    });

    return {
      agents, searchKw, filteredAgents,
      showModal, saving, openMenu, form, loading,
      quickInput, defaultAgentId, defaultAgentName,
      totalTools, totalKB,
      onQuickInput, onQuickKey, quickSend,
      toggleMenu, startChat, editAgent, deleteAgent,
      openCreate, closeModal, saveAgent, fmtDate,
      agentIconByTitle
    };
  }
};

/* ============================================================
   AgentDetailPage
   ============================================================ */
const AgentDetailPage = {
  template: `
    <div class="mx-auto w-full max-w-6xl px-4 pb-12 pt-6 sm:px-6 sm:pt-8">
      <!-- 页面标题 + 操作 -->
      <header class="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex items-center gap-3">
          <button
            @click="goBack"
            class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg border border-border bg-card text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            title="返回"
          >
            <i data-lucide="arrow-left" class="h-4 w-4"></i>
          </button>
          <div class="flex items-center gap-3">
            <div class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-primary font-semibold text-primary-foreground ai-avatar-ring">
              {{ (form.title || '?').charAt(0).toUpperCase() }}
            </div>
            <div class="min-w-0">
              <h1 class="truncate text-xl font-semibold tracking-tight text-foreground">{{ form.title || '未命名 Agent' }}</h1>
              <p class="mt-0.5 text-xs text-muted-foreground">编辑 Agent 配置 · ID <span class="font-mono">{{ agent.id ? agent.id.slice(0, 8) : '-' }}</span></p>
            </div>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <span
            v-if="savedHint"
            class="inline-flex items-center gap-1 text-xs font-medium text-emerald-600"
          >
            <i data-lucide="check-circle-2" class="h-3.5 w-3.5"></i>已保存
          </span>
          <button
            @click="deleteAgent"
            class="inline-flex h-9 items-center justify-center gap-1.5 rounded-md border border-destructive/30 bg-transparent px-3.5 text-sm font-medium text-destructive transition-colors hover:bg-destructive/10"
          >
            <i data-lucide="trash-2" class="h-4 w-4"></i>
            删除
          </button>
          <button
            @click="save"
            :disabled="saving"
            class="inline-flex h-9 items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground shadow-sm transition-all hover:bg-primary/90 active:scale-95 disabled:opacity-50"
          >
            <i v-if="saving" data-lucide="loader-2" class="h-4 w-4 animate-spin"></i>
            <i v-else data-lucide="check" class="h-4 w-4"></i>
            {{ saving ? '保存中...' : '保存修改' }}
          </button>
        </div>
      </header>

      <!-- 加载骨架屏 -->
      <div v-if="loading" class="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <div class="space-y-6 lg:col-span-2">
          <div v-for="n in 3" :key="'skel-' + n" class="rounded-xl border border-border bg-card p-5 shadow-sm">
            <div class="mb-4 h-4 w-24 animate-pulse rounded bg-muted"></div>
            <div class="h-10 w-full animate-pulse rounded-md bg-muted"></div>
            <div class="mt-3 h-10 w-3/4 animate-pulse rounded-md bg-muted"></div>
          </div>
        </div>
        <div class="rounded-xl border border-border bg-card p-5 shadow-sm">
          <div class="h-4 w-20 animate-pulse rounded bg-muted"></div>
          <div class="mt-4 h-12 w-12 animate-pulse rounded-xl bg-muted"></div>
          <div class="mt-4 h-3 w-full animate-pulse rounded bg-muted"></div>
          <div class="mt-2 h-3 w-2/3 animate-pulse rounded bg-muted"></div>
        </div>
      </div>

      <div v-else class="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <!-- 左栏：配置表单 -->
        <div class="space-y-6 lg:col-span-2">
          <!-- 基础信息 -->
          <section class="rounded-xl border border-border bg-card p-5 shadow-sm">
            <div class="mb-4 flex items-center gap-2 text-sm font-semibold text-foreground">
              <span class="flex h-6 w-6 items-center justify-center rounded-md bg-blue-50 text-primary"><i data-lucide="file-text" class="h-3.5 w-3.5"></i></span>
              基础信息
            </div>
            <div class="space-y-4">
              <label class="block text-sm font-medium text-foreground">
                名称
                <div class="relative mt-1.5">
                  <i data-lucide="bot" class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"></i>
                  <input
                    v-model="form.title"
                    maxlength="50"
                    placeholder="Agent 名称"
                    class="block h-10 w-full rounded-md border border-input bg-muted pl-9 pr-3 text-sm text-foreground outline-none transition-all placeholder:text-muted-foreground focus:border-primary focus:ring-2 focus:ring-ring/30"
                  >
                </div>
                <span class="mt-1 block text-xs text-muted-foreground">{{ form.title.length }}/50</span>
              </label>
              <label class="block text-sm font-medium text-foreground">
                描述
                <div class="relative mt-1.5">
                  <i data-lucide="align-left" class="pointer-events-none absolute left-3 top-3 h-4 w-4 -translate-y-1/2 text-muted-foreground"></i>
                  <textarea
                    v-model="form.description"
                    maxlength="200"
                    rows="2"
                    placeholder="简短描述此 Agent 的功能"
                    class="block w-full resize-y rounded-md border border-input bg-muted py-2.5 pl-9 pr-3 text-sm leading-relaxed text-foreground outline-none transition-all placeholder:text-muted-foreground focus:border-primary focus:ring-2 focus:ring-ring/30"
                  ></textarea>
                </div>
                <span class="mt-1 block text-xs text-muted-foreground">最多 200 字 · 当前 {{ form.description.length }}</span>
              </label>
              <label class="block text-sm font-medium text-foreground">
                系统提示词 (Prompt)
                <textarea
                  v-model="form.prompt"
                  rows="8"
                  placeholder="定义 Agent 的行为和角色"
                  class="mt-1.5 block w-full resize-y rounded-md border border-input bg-muted px-3 py-2.5 font-mono text-[13px] leading-relaxed text-foreground outline-none transition-all placeholder:text-muted-foreground focus:border-primary focus:ring-2 focus:ring-ring/30"
                ></textarea>
                <span class="mt-1 block text-xs text-muted-foreground">系统提示词决定 Agent 的行为方式和回答风格</span>
              </label>
            </div>
          </section>

          <!-- 可用工具 -->
          <section class="rounded-xl border border-border bg-card p-5 shadow-sm">
            <div class="mb-1 flex items-center justify-between">
              <div class="flex items-center gap-2 text-sm font-semibold text-foreground">
                <span class="flex h-6 w-6 items-center justify-center rounded-md bg-blue-50 text-primary"><i data-lucide="wrench" class="h-3.5 w-3.5"></i></span>
                可用工具
              </div>
              <span class="text-xs text-muted-foreground">已选 {{ form.tools.length }} / {{ tools.length }}</span>
            </div>
            <p class="mb-4 text-xs text-muted-foreground">开启后 Agent 在对话中可调用对应工具</p>
            <div v-if="tools.length === 0" class="rounded-lg border border-dashed border-border py-8 text-center text-sm text-muted-foreground">
              暂无可用工具
            </div>
            <div v-else class="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
              <button
                v-for="t in tools"
                :key="t.name"
                type="button"
                @click="toggleTool(t.name)"
                :class="isToolChecked(t.name)
                  ? 'border-primary/40 bg-blue-50'
                  : 'border-border bg-background hover:border-primary/30 hover:bg-muted'"
                class="flex items-start gap-2.5 rounded-lg border p-3 text-left transition-all"
              >
                <span
                  :class="isToolChecked(t.name) ? 'border-primary bg-primary text-primary-foreground' : 'border-border text-muted-foreground'"
                  class="mt-0.5 flex h-5 w-5 flex-shrink-0 items-center justify-center rounded-md border transition-colors"
                >
                  <i v-if="isToolChecked(t.name)" data-lucide="check" class="h-3 w-3"></i>
                </span>
                <span class="min-w-0 flex-1">
                  <span class="block text-sm font-medium text-foreground">{{ t.name }}</span>
                  <span class="mt-0.5 block text-xs leading-relaxed text-muted-foreground line-clamp-2">{{ t.description }}</span>
                </span>
              </button>
            </div>
          </section>

          <!-- 模型参数 -->
          <section class="rounded-xl border border-border bg-card p-5 shadow-sm">
            <div class="mb-4 flex items-center gap-2 text-sm font-semibold text-foreground">
              <span class="flex h-6 w-6 items-center justify-center rounded-md bg-blue-50 text-primary"><i data-lucide="sliders-horizontal" class="h-3.5 w-3.5"></i></span>
              模型参数
            </div>
            <div class="grid grid-cols-1 gap-x-6 gap-y-5 sm:grid-cols-2">
              <label class="block text-sm font-medium text-foreground">
                Temperature
                <div class="mt-2.5 flex items-center gap-3">
                  <input
                    type="range"
                    min="0"
                    max="2"
                    step="0.1"
                    v-model.number="form.temperature"
                    class="h-1.5 w-full cursor-pointer accent-primary"
                  >
                  <span class="flex h-7 w-11 flex-shrink-0 items-center justify-center rounded-md bg-muted font-mono text-xs text-foreground">{{ form.temperature }}</span>
                </div>
                <span class="mt-1.5 block text-xs text-muted-foreground">越高越有创造性 (0-2)</span>
              </label>
              <label class="block text-sm font-medium text-foreground">
                Max Tokens
                <input
                  type="number"
                  min="1"
                  max="32000"
                  v-model.number="form.max_tokens"
                  placeholder="1024"
                  class="mt-1.5 block h-10 w-full rounded-md border border-input bg-muted px-3 text-sm text-foreground outline-none transition-all placeholder:text-muted-foreground focus:border-primary focus:ring-2 focus:ring-ring/30"
                >
                <span class="mt-1.5 block text-xs text-muted-foreground">最大输出 token 数</span>
              </label>
              <label class="block text-sm font-medium text-foreground">
                最大迭代次数
                <div class="relative mt-1.5">
                  <select
                    v-model.number="form.max_iterations"
                    class="block h-10 w-full appearance-none rounded-md border border-input bg-muted pl-3 pr-9 text-sm text-foreground outline-none transition-all focus:border-primary focus:ring-2 focus:ring-ring/30"
                  >
                    <option v-for="n in 10" :key="n" :value="n">{{ n }}</option>
                  </select>
                  <i data-lucide="chevron-down" class="pointer-events-none absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"></i>
                </div>
                <span class="mt-1.5 block text-xs text-muted-foreground">工具调用最大循环次数</span>
              </label>
            </div>
          </section>
        </div>

        <!-- 右栏：预览 + 对话测试 -->
        <div class="space-y-6 lg:sticky lg:top-6 lg:self-start">
          <!-- Agent 概览 -->
          <section class="rounded-xl border border-border bg-card p-5 shadow-sm">
            <div class="mb-4 flex items-center gap-2 text-sm font-semibold text-foreground">
              <span class="flex h-6 w-6 items-center justify-center rounded-md bg-blue-50 text-primary"><i data-lucide="layout-dashboard" class="h-3.5 w-3.5"></i></span>
              Agent 概览
            </div>
            <div class="mb-4 flex items-center gap-3">
              <div class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary">
                <i :data-lucide="agentIconByTitle(form.title)" class="h-6 w-6"></i>
              </div>
              <div class="min-w-0">
                <div class="truncate text-base font-semibold text-foreground">{{ form.title || '未命名' }}</div>
                <div class="text-xs text-muted-foreground">{{ agent.status === 'active' ? '已启用' : '状态未知' }}</div>
              </div>
            </div>
            <p class="mb-4 text-sm leading-relaxed text-muted-foreground line-clamp-3">{{ form.description || '暂无描述' }}</p>
            <dl class="space-y-2 text-sm">
              <div class="flex items-center justify-between"><dt class="text-muted-foreground">ID</dt><dd class="font-mono text-xs text-foreground">{{ agent.id ? agent.id.slice(0, 8) : '-' }}</dd></div>
              <div class="flex items-center justify-between"><dt class="text-muted-foreground">创建</dt><dd class="text-foreground">{{ fmtDate(agent.created_at) }}</dd></div>
              <div class="flex items-center justify-between"><dt class="text-muted-foreground">更新</dt><dd class="text-foreground">{{ fmtDate(agent.updated_at) }}</dd></div>
              <div class="flex items-center justify-between"><dt class="text-muted-foreground">Temperature</dt><dd class="font-mono text-foreground">{{ form.temperature }}</dd></div>
              <div class="flex items-center justify-between"><dt class="text-muted-foreground">Max Tokens</dt><dd class="font-mono text-foreground">{{ form.max_tokens }}</dd></div>
              <div class="flex items-center justify-between"><dt class="text-muted-foreground">迭代次数</dt><dd class="font-mono text-foreground">{{ form.max_iterations }}</dd></div>
            </dl>
            <div v-if="form.tools.length > 0" class="mt-4 border-t border-border pt-3">
              <div class="mb-2 text-xs text-muted-foreground">已启用工具</div>
              <div class="flex flex-wrap gap-1.5">
                <span
                  v-for="t in form.tools"
                  :key="t"
                  class="inline-flex items-center gap-1 rounded-md bg-blue-50 px-2 py-0.5 text-[11px] font-medium text-primary"
                >
                  <i data-lucide="wrench" class="h-3 w-3"></i>{{ t }}
                </span>
              </div>
            </div>
          </section>

          <!-- Prompt 预览 -->
          <section v-if="form.prompt" class="rounded-xl border border-border bg-card p-5 shadow-sm">
            <div class="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">
              <span class="flex h-6 w-6 items-center justify-center rounded-md bg-blue-50 text-primary"><i data-lucide="message-square-text" class="h-3.5 w-3.5"></i></span>
              系统提示词
            </div>
            <pre class="max-h-64 overflow-y-auto whitespace-pre-wrap rounded-lg bg-muted p-3 font-mono text-xs leading-relaxed text-foreground">{{ form.prompt }}</pre>
          </section>

          <!-- 对话测试 -->
          <section class="flex flex-col overflow-hidden rounded-xl border border-border bg-card shadow-sm">
            <div class="border-b border-border px-5 py-4">
              <div class="flex items-center gap-2 text-sm font-semibold text-foreground">
                <span class="flex h-6 w-6 items-center justify-center rounded-md bg-blue-50 text-primary"><i data-lucide="flask-conical" class="h-3.5 w-3.5"></i></span>
                对话测试
              </div>
              <p class="mt-1 text-xs text-muted-foreground">在保存前快速验证 Agent 效果</p>
            </div>
            <div
              ref="testChatScroll"
              class="flex-1 space-y-3 overflow-y-auto px-4 py-4"
              style="min-height: 200px; max-height: 320px;"
            >
              <div v-if="testMessages.length === 0" class="flex flex-col items-center justify-center py-8 text-center text-xs text-muted-foreground">
                <i data-lucide="message-circle" class="mb-2 h-6 w-6 opacity-40"></i>
                输入消息测试 Agent 效果
              </div>
              <div v-for="m in testMessages" :key="m.id" class="flex" :class="m.role === 'user' ? 'justify-end' : 'justify-start'">
                <div v-if="m.role === 'user'" class="max-w-[85%] rounded-2xl rounded-tr-md bg-primary px-3.5 py-2 text-sm text-primary-foreground">
                  {{ m.content }}
                </div>
                <div v-else class="max-w-[85%] rounded-2xl rounded-tl-md border border-border bg-background px-3.5 py-2 text-sm text-foreground ai-card-shadow">
                  <div v-if="m.content" class="bubble-md" v-html="m.html || renderMarkdown(m.content)"></div>
                  <span v-else-if="m.streaming" class="inline-flex items-center gap-1.5 text-muted-foreground">
                    <span class="typing-dots flex gap-1">
                      <span class="h-1.5 w-1.5 rounded-full bg-primary"></span>
                      <span class="h-1.5 w-1.5 rounded-full bg-primary"></span>
                      <span class="h-1.5 w-1.5 rounded-full bg-primary"></span>
                    </span>
                  </span>
                  <span v-else class="text-muted-foreground italic">空响应</span>
                </div>
              </div>
            </div>
            <div class="border-t border-border p-3">
              <div class="relative">
                <textarea
                  ref="testInputEl"
                  v-model="testMsg"
                  rows="1"
                  placeholder="输入测试消息..."
                  class="w-full resize-none rounded-lg border border-input bg-muted py-2.5 pl-3.5 pr-11 text-sm text-foreground outline-none transition-all placeholder:text-muted-foreground focus:border-primary focus:ring-2 focus:ring-ring/30"
                  style="min-height: 40px; max-height: 96px;"
                  @keydown="onTestKey"
                  @input="onTestInput"
                ></textarea>
                <button
                  @click="sendTest"
                  :disabled="testStreaming || !testMsg.trim()"
                  title="发送"
                  class="absolute bottom-2 right-2 flex h-7 w-7 items-center justify-center rounded-md bg-primary text-primary-foreground transition-all hover:bg-primary/90 active:scale-95 disabled:cursor-not-allowed disabled:opacity-40"
                >
                  <i data-lucide="arrow-up" class="h-3.5 w-3.5"></i>
                </button>
              </div>
            </div>
          </section>

          <!-- 快捷操作 -->
          <router-link
            :to="'/chat?agent_id=' + (agent.id || '')"
            class="flex h-10 items-center justify-center gap-1.5 rounded-md bg-primary text-sm font-medium text-primary-foreground shadow-sm transition-all hover:bg-primary/90 active:scale-95"
          >
            <i data-lucide="message-circle" class="h-4 w-4"></i>
            开始完整对话
          </router-link>
        </div>
      </div>
    </div>
  `,
  setup() {
    const route = useRoute();
    const router = useRouter();
    const agentId = route.query.id;

    const agent = reactive({});
    const tools = ref([]);
    const loading = ref(true);
    const saving = ref(false);
    const savedHint = ref(false);
    const form = reactive({
      title: '',
      description: '',
      prompt: '',
      tools: [],
      temperature: 0.7,
      max_tokens: 1024,
      max_iterations: 5
    });

    // 对话测试
    const testMessages = ref([]);
    const testMsg = ref('');
    const testStreaming = ref(false);
    const testInputEl = ref(null);
    const testChatScroll = ref(null);

    async function loadTools() {
      try {
        const r = await authFetch(API_BASE + '/tools');
        const d = await r.json();
        tools.value = (d.data && d.data.tools) || [];
      } catch (e) { tools.value = []; }
    }

    async function loadAgent() {
      if (!agentId) { router.replace('/agents'); return; }
      try {
        const r = await authFetch(API_BASE + '/agents/' + agentId);
        const d = await r.json();
        if (d.code !== 0 || !d.data) {
          showToast('Agent 不存在');
          setTimeout(() => router.replace('/agents'), 1500);
          return;
        }
        Object.assign(agent, d.data);
        fillForm(agent);
      } catch (e) {
        showToast('加载失败');
      } finally {
        loading.value = false;
      }
    }

    function fillForm(a) {
      form.title = a.title || '';
      form.description = a.description || '';
      form.prompt = a.prompt || '';
      form.tools = Array.isArray(a.tools) ? [...a.tools] : [];
      form.temperature = a.temperature != null ? a.temperature : 0.7;
      form.max_tokens = a.max_tokens || 1024;
      form.max_iterations = a.max_iterations || 5;
    }

    function isToolChecked(name) {
      return form.tools.includes(name);
    }
    function toggleTool(name) {
      const i = form.tools.indexOf(name);
      if (i >= 0) form.tools.splice(i, 1);
      else form.tools.push(name);
    }

    async function save() {
      if (!form.title.trim()) { showToast('请填写名称'); return; }
      if (!form.prompt.trim()) { showToast('请填写系统提示词'); return; }
      saving.value = true;
      try {
        const body = {
          title: form.title.trim(),
          description: form.description.trim(),
          prompt: form.prompt.trim(),
          tools: form.tools,
          temperature: form.temperature,
          max_tokens: form.max_tokens,
          max_iterations: form.max_iterations
        };
        const r = await authFetch(API_BASE + '/agents/' + agentId, {
          method: 'PUT',
          body: JSON.stringify(body)
        });
        const d = await r.json();
        if (d.code === 0) {
          Object.assign(agent, d.data || {});
          savedHint.value = true;
          setTimeout(() => { savedHint.value = false; }, 2000);
        } else {
          showToast(d.message || '保存失败');
        }
      } catch (e) {
        showToast('网络错误');
      } finally {
        saving.value = false;
      }
    }

    async function deleteAgent() {
      if (!agent.title) return;
      if (!confirm('确认删除 "' + agent.title + '"？删除后不可恢复。')) return;
      try {
        const r = await authFetch(API_BASE + '/agents/' + agentId, { method: 'DELETE' });
        const d = await r.json();
        if (d.code === 0) {
          router.replace('/agents');
        } else {
          showToast(d.message || '删除失败');
        }
      } catch (e) {}
    }

    function goBack() {
      router.push('/agents');
    }

    function onTestInput(e) {
      autoResize(e.target, 120);
    }

    function onTestKey(e) {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        sendTest();
      }
    }

    function scrollTestBottom() {
      nextTick(() => {
        if (testChatScroll.value) testChatScroll.value.scrollTop = testChatScroll.value.scrollHeight;
      });
    }

    async function sendTest() {
      const msg = testMsg.value.trim();
      if (!msg || testStreaming.value) return;
      testMsg.value = '';
      if (testInputEl.value) testInputEl.value.style.height = 'auto';
      testMessages.value.push({ id: Date.now(), role: 'user', content: msg });
      const aiId = Date.now() + 1;
      testMessages.value.push({ id: aiId, role: 'assistant', content: '', html: '', streaming: true });
      scrollTestBottom();

      testStreaming.value = true;
      try {
        const resp = await authFetch(API_BASE + '/chat/completion', {
          method: 'POST',
          body: JSON.stringify({ agent_id: agentId, message: msg })
        });
        if (!resp.ok) {
          let errMsg = '请求失败: ' + resp.status;
          try {
            const d = await resp.json();
            if (d.message) errMsg = d.message;
          } catch (e) {}
          throw new Error(errMsg);
        }

        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        let eventType = '';
        let dataStr = '';
        let fullText = '';

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n');
          buffer = lines.pop() || '';

          for (const line of lines) {
            if (line.startsWith('event: ')) {
              eventType = line.slice(7).trim();
            } else if (line.startsWith('data: ')) {
              dataStr = line.slice(6);
            } else if (line === '' && eventType) {
              let data;
              try { data = JSON.parse(dataStr); } catch (e) { data = dataStr; }

              if (eventType === 'chunk' && data && data.content) {
                fullText += data.content;
                const idx = testMessages.value.findIndex(m => m.id === aiId);
                if (idx >= 0) {
                  testMessages.value[idx].content = fullText;
                  testMessages.value[idx].html = renderMarkdown(fullText);
                }
                scrollTestBottom();
              } else if (eventType === 'error') {
                const errMsg = (typeof data === 'object' && data.message) ? data.message : (data || '生成失败');
                throw new Error(errMsg);
              }

              eventType = '';
              dataStr = '';
            }
          }
        }

        const idx = testMessages.value.findIndex(m => m.id === aiId);
        if (idx >= 0) testMessages.value[idx].streaming = false;
      } catch (e) {
        if (e.message === 'unauthorized') return;
        const idx = testMessages.value.findIndex(m => m.id === aiId);
        if (idx >= 0) {
          testMessages.value[idx].content = '请求失败: ' + e.message;
          testMessages.value[idx].html = '';
          testMessages.value[idx].streaming = false;
        }
      } finally {
        testStreaming.value = false;
        scrollTestBottom();
      }
    }

    // Tailwind 重渲染会替换 <i data-lucide> 节点,图标丢失时需重新初始化。
    // 必须 flush:'post' —— 默认 flush:'pre' 在重渲染之前触发,loadIcons 转换的是旧 DOM。
    // loading 也纳入监听:数据加载完成后 v-else 分区卡片才渲染,需要再转一次图标。
    watch(
      [() => form.title, () => form.tools, tools, () => loading.value],
      () => { loadIcons(); },
      { deep: true, flush: 'post' }
    );

    onMounted(() => {
      if (!agentId) { router.replace('/agents'); return; }
      loadTools();
      loadAgent();
      loadIcons();
    });

    return {
      agent, tools, form, loading, saving, savedHint,
      save, deleteAgent, goBack, fmtDate,
      testMessages, testMsg, testStreaming, testInputEl, testChatScroll,
      onTestKey, onTestInput, sendTest,
      isToolChecked, toggleTool, renderMarkdown, agentIconByTitle
    };
  }
};

/* ============================================================
   ChatPage
   ============================================================ */
const ChatPage = {
  template: `
    <div class="flex h-screen w-screen overflow-hidden bg-background text-foreground font-sans antialiased">

      <!-- 左栏:历史会话 -->
      <aside
        class="flex-shrink-0 border-r border-border bg-card flex flex-col transition-[width] duration-200 ease-out"
        :class="sidebarCollapsed ? 'w-16' : 'w-72'"
      >
        <!-- 折叠态:窄条 -->
        <div v-if="sidebarCollapsed" class="flex-1 flex flex-col items-center py-3 gap-2">
          <button
            class="h-9 w-9 rounded-xl flex items-center justify-center bg-primary text-primary-foreground ai-glow hover:bg-primary/90 hover:scale-105 active:scale-95 transition disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:scale-100"
            @click="newSession"
            :disabled="streaming || !agentId"
            title="新建会话"
            data-dom-id="chat-new-session-collapsed"
          >
            <i data-lucide="plus" class="h-4 w-4"></i>
          </button>
          <div class="h-px w-6 bg-border my-1"></div>
          <button
            class="h-9 w-9 rounded-xl flex items-center justify-center hover:bg-muted text-muted-foreground hover:text-foreground transition"
            @click="sidebarCollapsed = false"
            title="展开侧栏"
            data-dom-id="chat-sidebar-expand"
          >
            <i data-lucide="panel-left-open" class="h-4 w-4"></i>
          </button>
          <div class="flex-1"></div>
          <button
            v-if="email"
            class="h-9 w-9 rounded-full bg-primary/15 text-primary flex items-center justify-center text-xs font-semibold hover:bg-primary/25 transition cursor-pointer"
            @click.stop="userMenuOpen = !userMenuOpen"
            :title="email"
            data-dom-id="chat-user-menu-collapsed"
          >
            {{ userInitial }}
          </button>
        </div>

        <!-- 展开态:完整侧栏 -->
        <template v-else>
          <!-- 品牌区 -->
          <div class="h-16 flex-shrink-0 px-4 flex items-center justify-between border-b border-border">
            <div class="flex items-center gap-2.5 cursor-pointer group" @click="$router.push('/agents')" data-dom-id="chat-brand">
              <span class="relative flex h-8 w-8 items-center justify-center rounded-lg bg-primary text-primary-foreground ai-avatar-ring">
                <i data-lucide="bot" class="h-4 w-4 ai-float"></i>
              </span>
              <div class="flex flex-col leading-tight">
                <span class="font-semibold text-sm tracking-tight">ansmeee</span>
                <span class="text-[10px] text-muted-foreground">Agent Platform</span>
              </div>
            </div>
            <button
              class="h-8 w-8 flex items-center justify-center rounded-lg hover:bg-muted text-muted-foreground hover:text-foreground transition"
              @click="sidebarCollapsed = true"
              title="折叠侧栏"
              data-dom-id="chat-sidebar-collapse"
            >
              <i data-lucide="panel-left-close" class="h-4 w-4"></i>
            </button>
          </div>

          <!-- 新建对话 -->
          <div class="p-3 border-b border-border">
            <button
              class="w-full flex items-center justify-center gap-2 px-4 py-2.5 bg-primary text-primary-foreground rounded-xl text-sm font-semibold hover:shadow-lg hover:-translate-y-px active:scale-[0.98] ai-glow transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:translate-y-0"
              @click="newSession"
              :disabled="streaming || !agentId"
              data-dom-id="chat-new-session"
            >
              <i data-lucide="plus" class="h-4 w-4"></i>
              <span>新对话</span>
            </button>
          </div>

          <!-- 会话历史列表 -->
          <div class="flex-1 overflow-y-auto p-3 space-y-0.5">
            <div class="flex items-center justify-between px-2 py-2">
              <span class="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">历史会话</span>
              <span v-if="sessions.length > 0" class="text-[10px] text-muted-foreground bg-muted px-1.5 py-0.5 rounded-full">{{ sessions.length }}</span>
            </div>
            <div v-if="sessions.length === 0" class="text-xs text-muted-foreground px-3 py-6 text-center">
              <i data-lucide="message-square-dashed" class="h-5 w-5 mx-auto mb-1.5 opacity-50"></i>
              暂无会话
            </div>
            <div
              v-for="s in sessions"
              :key="s.id"
              class="group relative flex items-center gap-1 rounded-lg cursor-pointer transition-all duration-150"
              :class="s.id === currentSession
                ? 'bg-brand-soft text-foreground border border-primary/25 shadow-sm'
                : 'text-muted-foreground hover:bg-muted hover:text-foreground border border-transparent'"
              @click="selectSession(s.id)"
              :data-dom-id="'chat-session-' + s.id"
            >
              <!-- active 左侧品牌色小条 -->
              <span
                v-if="s.id === currentSession"
                class="absolute left-0 top-1.5 bottom-1.5 w-0.5 bg-primary rounded-r-full"
              ></span>
              <i
                data-lucide="message-circle"
                class="h-3.5 w-3.5 flex-shrink-0 ml-2.5"
                :class="s.id === currentSession ? 'text-primary' : 'text-muted-foreground/60'"
              ></i>
              <div class="flex-1 min-w-0 px-2 py-2 text-[13px] truncate font-medium">
                {{ s.title || '新会话' }}
              </div>
              <button
                class="opacity-0 group-hover:opacity-100 h-7 w-7 mr-1 flex items-center justify-center rounded-md hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition"
                @click.stop="deleteSession(s)"
                title="删除会话"
              >
                <i data-lucide="trash-2" class="h-3.5 w-3.5"></i>
              </button>
            </div>
          </div>

          <!-- 用户信息 -->
          <div class="p-3 border-t border-border" v-if="email">
            <div class="relative">
              <div
                class="flex items-center gap-2.5 cursor-pointer rounded-lg p-2 hover:bg-muted transition"
                @click.stop="userMenuOpen = !userMenuOpen"
                data-dom-id="chat-user-menu"
              >
                <div class="h-8 w-8 rounded-full bg-primary text-primary-foreground flex items-center justify-center text-xs font-semibold flex-shrink-0 ai-avatar-ring">
                  {{ userInitial }}
                </div>
                <div class="flex-1 min-w-0">
                  <div class="text-xs font-medium text-foreground truncate">{{ email }}</div>
                  <div class="text-[10px] text-muted-foreground">已登录</div>
                </div>
                <i data-lucide="chevron-up" class="h-3.5 w-3.5 text-muted-foreground"></i>
              </div>
              <div
                v-if="userMenuOpen"
                class="absolute bottom-full left-0 right-0 mb-1.5 rounded-xl border border-border bg-card overflow-hidden ai-card-shadow"
                @click.stop
              >
                <button class="w-full flex items-center gap-2 px-3 py-2.5 text-sm text-foreground hover:bg-muted transition text-left" @click="goUser">
                  <i data-lucide="user" class="h-3.5 w-3.5 text-muted-foreground"></i>
                  <span>个人中心</span>
                </button>
                <div class="h-px bg-border mx-2"></div>
                <button class="w-full flex items-center gap-2 px-3 py-2.5 text-sm text-foreground hover:bg-destructive/10 hover:text-destructive transition text-left" @click="logout">
                  <i data-lucide="log-out" class="h-3.5 w-3.5"></i>
                  <span>退出登录</span>
                </button>
              </div>
            </div>
          </div>
        </template>
      </aside>

      <!-- 中栏:对话主区 -->
      <main class="flex-1 flex flex-col min-w-0 relative bg-background">

        <!-- Agent 顶栏 -->
        <header class="h-16 flex-shrink-0 border-b border-border bg-card/85 backdrop-blur-md px-4 md:px-6 flex items-center justify-between gap-4 sticky top-0 z-10">

          <!-- 左:Agent 选择器 -->
          <div class="flex items-center gap-3 min-w-0">
            <div class="relative h-10 w-10 rounded-xl bg-primary flex items-center justify-center ai-avatar-ring flex-shrink-0">
              <i :data-lucide="currentAgentIcon" class="h-5 w-5 text-primary-foreground ai-float"></i>
              <span class="absolute -bottom-0.5 -right-0.5 w-3 h-3 rounded-full bg-success border-2 border-card ai-pulse"></span>
            </div>
            <div class="min-w-0">
              <div class="text-sm font-semibold text-foreground truncate flex items-center gap-1.5" data-dom-id="chat-agent-title">
                {{ currentAgentName || '选择 Agent' }}
              </div>
              <div class="text-[11px] text-muted-foreground truncate flex items-center gap-1">
                <span>在线 · {{ currentAgent.description || '准备就绪' }}</span>
              </div>
            </div>
            <button
              class="ml-1 h-8 w-8 flex items-center justify-center rounded-lg hover:bg-muted text-muted-foreground hover:text-foreground transition"
              @click.stop="agentPickerOpen = !agentPickerOpen"
              title="切换 Agent"
              data-dom-id="chat-agent-switcher"
            >
              <i data-lucide="chevron-down" class="h-4 w-4 transition-transform duration-200" :class="{ 'rotate-180': agentPickerOpen }"></i>
            </button>
          </div>

          <!-- 右:操作 -->
          <div class="flex items-center gap-2 flex-shrink-0">
            <button
              class="hidden sm:flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium transition"
              :class="kbInject ? 'bg-primary/12 text-primary border border-primary/25' : 'text-muted-foreground bg-muted hover:bg-muted/80 border border-transparent'"
              @click="kbInject = !kbInject"
              title="知识库注入"
            >
              <i data-lucide="database" class="h-3.5 w-3.5"></i>
              <span class="hidden md:inline">知识库</span>
            </button>
            <button
              class="hidden sm:flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium transition"
              :class="deepThink ? 'bg-primary/12 text-primary border border-primary/25' : 'text-muted-foreground bg-muted hover:bg-muted/80 border border-transparent'"
              @click="deepThink = !deepThink"
              title="深度思考"
            >
              <i data-lucide="brain" class="h-3.5 w-3.5"></i>
              <span class="hidden md:inline">深度思考</span>
            </button>
            <button
              class="h-8 w-8 flex items-center justify-center rounded-lg hover:bg-muted text-muted-foreground hover:text-foreground transition"
              @click="$router.push('/agents')"
              title="Agent 管理"
            >
              <i data-lucide="settings" class="h-4 w-4"></i>
            </button>
          </div>

          <!-- Agent 下拉菜单(挂在 header 上,绝对定位) -->
          <div
            v-if="agentPickerOpen"
            class="absolute top-[calc(100%+8px)] left-4 md:left-6 w-80 max-h-96 overflow-y-auto rounded-xl border border-border bg-card ai-card-shadow z-20 p-1"
            @click.stop
          >
            <div class="px-3 py-2 border-b border-border mb-1">
              <div class="text-xs font-medium text-foreground">切换 Agent</div>
              <div class="text-[11px] text-muted-foreground mt-0.5">选择一个智能体开始新对话</div>
            </div>
            <div v-if="agents.length === 0" class="p-6 text-center text-sm text-muted-foreground">
              暂无 Agent,请先在智能体页面创建
            </div>
            <button
              v-for="a in agents"
              :key="a.id"
              class="w-full flex items-start gap-3 px-3 py-2.5 rounded-lg hover:bg-muted transition text-left"
              :class="{ 'bg-primary/8': a.id === agentId }"
              @click="switchAgent(a.id)"
            >
              <div class="h-9 w-9 rounded-lg bg-primary/15 text-primary flex items-center justify-center flex-shrink-0">
                <i data-lucide="cpu" class="h-4 w-4"></i>
              </div>
              <div class="flex-1 min-w-0">
                <div class="text-sm font-medium text-foreground truncate">{{ a.title || '未命名' }}</div>
                <div class="text-xs text-muted-foreground line-clamp-2 mt-0.5">{{ a.description || '—' }}</div>
              </div>
              <i v-if="a.id === agentId" data-lucide="check" class="h-4 w-4 text-primary flex-shrink-0 mt-1"></i>
            </button>
          </div>
        </header>

        <!-- 错误提示 -->
        <div v-if="error" class="mx-4 md:mx-8 mt-3 flex items-center gap-2 px-4 py-2.5 rounded-xl border border-destructive/30 bg-destructive/8 text-destructive text-sm ai-card-shadow" data-dom-id="chat-error-banner">
          <i data-lucide="alert-circle" class="h-4 w-4 flex-shrink-0"></i>
          <span class="flex-1">{{ error }}</span>
          <button class="hover:opacity-80 rounded-md p-0.5 hover:bg-destructive/15" @click="error = ''" title="关闭">
            <i data-lucide="x" class="h-3.5 w-3.5"></i>
          </button>
        </div>

        <!-- 消息流 -->
        <div class="flex-1 overflow-y-auto" ref="messagesContainer" data-dom-id="chat-messages">
          <div class="max-w-3xl mx-auto px-4 md:px-8 py-8 space-y-7">

            <!-- 空状态:未选 session 也无消息 -->
            <div
              v-if="messages.length === 0 && !thinking"
              class="flex flex-col items-center justify-center text-center min-h-[60vh] py-12"
              data-dom-id="chat-empty-state"
            >
              <div class="relative w-20 h-20 rounded-2xl bg-primary/10 flex items-center justify-center mb-6 ai-float ai-avatar-ring ai-glow-soft">
                <i :data-lucide="currentAgentIcon" class="w-10 h-10 text-primary"></i>
                <span class="absolute -bottom-1 -right-1 w-5 h-5 rounded-full bg-card flex items-center justify-center ai-avatar-ring">
                  <span class="w-2 h-2 rounded-full bg-success ai-pulse"></span>
                </span>
              </div>
              <h1 class="text-2xl font-semibold text-foreground mb-2 tracking-tight">
                你好,我是 {{ currentAgentName || 'AI 助手' }}
              </h1>
              <p class="text-sm text-muted-foreground mb-8 max-w-md leading-relaxed">
                {{ currentAgent.description || '有什么可以帮你的吗?从下面的快速入口开始,或者直接输入问题。' }}
              </p>
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-3 w-full max-w-2xl">
                <button
                  v-for="chip in quickChips"
                  :key="chip.title"
                  class="quick-chip group flex items-start gap-3 px-4 py-3.5 rounded-xl border border-border bg-card text-left ai-card-hover transition-all"
                  @click="input = chip.prompt; $nextTick(() => inputEl && inputEl.focus())"
                >
                  <div class="h-9 w-9 rounded-lg bg-primary/12 text-primary flex items-center justify-center flex-shrink-0 group-hover:bg-primary/20 transition">
                    <i :data-lucide="chip.icon" class="h-4 w-4"></i>
                  </div>
                  <div class="flex-1 min-w-0">
                    <div class="text-sm font-semibold text-foreground mb-0.5">{{ chip.title }}</div>
                    <div class="text-xs text-muted-foreground leading-relaxed">{{ chip.desc }}</div>
                  </div>
                  <i data-lucide="arrow-up-right" class="h-3.5 w-3.5 text-muted-foreground opacity-0 group-hover:opacity-100 group-hover:text-primary transition-all mt-1"></i>
                </button>
              </div>
            </div>

            <!-- 消息列表 -->
            <template v-for="(m, i) in messages" :key="i">
              <!-- 用户消息 -->
              <div v-if="m.role === 'human'" class="flex gap-3 justify-end group">
                <div class="flex flex-col items-end gap-1 max-w-[80%]">
                  <div class="bubble-user px-4 py-2.5 rounded-2xl rounded-tr-md text-sm text-foreground whitespace-pre-wrap break-words" v-text="m.content"></div>
                  <span class="text-[11px] text-muted-foreground/80 px-1.5 mt-0.5">
                    {{ formatTime(m.created_at) || fmtTime(new Date()) }}
                  </span>
                </div>
                <div class="h-8 w-8 rounded-full bg-primary flex-shrink-0 flex items-center justify-center ai-avatar-ring">
                  <span class="text-xs font-semibold text-primary-foreground">{{ userInitial }}</span>
                </div>
              </div>

              <!-- AI 消息 -->
              <div v-else class="flex gap-3 group">
<div class="h-8 w-8 rounded-xl bg-primary flex-shrink-0 flex items-center justify-center ai-avatar-ring">
              <i :data-lucide="currentAgentIcon" class="h-4 w-4 text-primary-foreground"></i>
            </div>
            <div class="flex-1 min-w-0 space-y-2">
                  <!-- 名字 + 时间 -->
                  <div class="flex items-center gap-2 text-xs">
                    <span class="font-semibold text-foreground">{{ currentAgentName || 'AI' }}</span>
                    <span class="text-muted-foreground">{{ formatTime(m.created_at) || fmtTime(new Date()) }}</span>
                  </div>
                  <!-- 思考折叠区 -->
                  <div
                    v-if="m.thinking || (m.streaming && thinking && i === messages.length - 1)"
                    class="relative rounded-xl border border-border bg-card overflow-hidden"
                  >
                    <span class="absolute left-0 top-2 bottom-2 w-0.5 bg-primary rounded-r-full"></span>
                    <button
                      class="w-full flex items-center gap-2 pl-4 pr-3 py-2 text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-muted/40 transition"
                      @click="toggleThinking(i)"
                    >
                      <i
                        data-lucide="chevron-right"
                        class="h-3.5 w-3.5 transition-transform duration-200"
                        :class="{ 'rotate-90': thinkingOpen[i] }"
                      ></i>
                      <i data-lucide="brain" class="h-3.5 w-3.5 text-primary"></i>
                      <span>{{ m.streaming && thinking ? '深度思考中…' : '已深度思考' }}</span>
                      <span v-if="m.thinkingDuration" class="text-[10px] text-muted-foreground/80 ml-auto mr-1 tabular-nums">{{ m.thinkingDuration }}s</span>
                    </button>
                    <div v-if="thinkingOpen[i]" class="pl-4 pr-3 pb-3 pt-1 text-xs text-muted-foreground whitespace-pre-wrap border-t border-border bg-muted/30 max-h-72 overflow-y-auto leading-relaxed">
                      {{ m.thinking || '思考过程暂未推送…' }}
                    </div>
                  </div>
                  <!-- 正文 -->
                  <div
                    class="text-sm text-foreground leading-relaxed"
                    :class="{ 'streaming': m.streaming }"
                  >
                    <div v-if="m.content" class="bubble-md" v-html="renderMarkdown(m.content)"></div>
                    <span v-else-if="m.streaming" class="inline-flex items-center gap-1.5 text-muted-foreground py-1">
                      <span class="typing-dots flex gap-1">
                        <span class="w-1.5 h-1.5 rounded-full bg-primary"></span>
                        <span class="w-1.5 h-1.5 rounded-full bg-primary"></span>
                        <span class="w-1.5 h-1.5 rounded-full bg-primary"></span>
                      </span>
                    </span>
                    <span v-else class="text-muted-foreground italic">空响应</span>
                  </div>
                  <!-- 操作栏 -->
                  <div v-if="!m.streaming && m.content" class="flex items-center gap-0.5 pt-1 opacity-0 group-hover:opacity-100 transition-opacity">
                    <button
                      class="h-7 px-2 flex items-center gap-1 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground transition text-xs"
                      @click="copyMessage(m.content)"
                      title="复制"
                    >
                      <i data-lucide="copy" class="h-3.5 w-3.5"></i>
                      <span class="hidden lg:inline">复制</span>
                    </button>
                    <button
                      class="h-7 px-2 flex items-center gap-1 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground transition text-xs"
                      @click="regenerate(i)"
                      title="重新生成"
                    >
                      <i data-lucide="refresh-cw" class="h-3.5 w-3.5"></i>
                      <span class="hidden lg:inline">重新生成</span>
                    </button>
                    <button
                      class="h-7 px-2 flex items-center gap-1 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground transition text-xs"
                      @click="likeMessage(i)"
                      title="点赞"
                    >
                      <i data-lucide="thumbs-up" class="h-3.5 w-3.5"></i>
                    </button>
                  </div>
                </div>
              </div>
            </template>

            <!-- 独立 typing 指示器(没有 AI 消息在流式时) -->
            <div
              v-if="thinking && (messages.length === 0 || !messages[messages.length - 1].streaming)"
              class="flex gap-3"
              data-dom-id="chat-typing"
            >
              <div class="h-8 w-8 rounded-xl bg-primary flex-shrink-0 flex items-center justify-center ai-avatar-ring">
                <i :data-lucide="currentAgentIcon" class="h-4 w-4 text-primary-foreground ai-pulse"></i>
              </div>
              <div class="flex-1 min-w-0 space-y-2">
                <div class="flex items-center gap-2 text-xs">
                  <span class="font-semibold text-foreground">{{ currentAgentName || 'AI' }}</span>
                  <span class="text-muted-foreground">思考中</span>
                </div>
                <div class="inline-flex items-center gap-2 px-4 py-2.5 rounded-2xl rounded-tl-md border border-border bg-card ai-card-shadow">
                  <span class="typing-dots flex gap-1">
                    <span class="w-1.5 h-1.5 rounded-full bg-primary"></span>
                    <span class="w-1.5 h-1.5 rounded-full bg-primary"></span>
                    <span class="w-1.5 h-1.5 rounded-full bg-primary"></span>
                  </span>
                  <span class="text-xs text-muted-foreground ml-1">正在生成回复</span>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 底部输入区 -->
        <div class="flex-shrink-0 p-4 md:px-8 pb-6">
          <div class="max-w-3xl mx-auto">
            <div
              class="relative rounded-2xl border border-border bg-card ai-card-shadow transition-all focus-within:border-primary/40 focus-within:shadow-[0_0_0_4px_rgba(59,130,246,0.12),0_4px_16px_-2px_rgba(59,130,246,0.18)]"
            >
              <textarea
                ref="inputEl"
                v-model="input"
                placeholder="输入消息,Shift + Enter 换行"
                rows="1"
                class="w-full bg-transparent px-4 py-3.5 pr-28 text-sm text-foreground placeholder:text-muted-foreground/70 resize-none outline-none"
                style="min-height: 52px; max-height: 160px;"
                @keydown="onKey"
                @input="onInput"
                data-dom-id="chat-input"
              ></textarea>
              <div class="absolute right-2.5 bottom-2.5 flex items-center gap-1">
                <button
                  class="hidden sm:flex h-8 w-8 items-center justify-center rounded-lg transition"
                  :class="deepThink ? 'bg-primary/12 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'"
                  title="深度思考"
                  @click="deepThink = !deepThink"
                >
                  <i data-lucide="brain" class="h-4 w-4"></i>
                </button>
                <button
                  class="hidden sm:flex h-8 w-8 items-center justify-center rounded-lg transition"
                  :class="kbInject ? 'bg-primary/12 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'"
                  title="知识库注入"
                  @click="kbInject = !kbInject"
                >
                  <i data-lucide="database" class="h-4 w-4"></i>
                </button>
                <button
                  class="h-9 w-9 flex items-center justify-center rounded-lg bg-primary text-primary-foreground hover:shadow-lg hover:-translate-y-px active:scale-95 ai-glow transition-all duration-150 disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:translate-y-0"
                  @click="send"
                  :disabled="sendDisabled || streaming"
                  title="发送"
                  data-dom-id="chat-send-btn"
                >
                  <i data-lucide="send" class="h-4 w-4"></i>
                </button>
              </div>
            </div>
            <div class="mt-2.5 text-center text-[11px] text-muted-foreground flex items-center justify-center gap-1.5">
              <i data-lucide="info" class="h-3 w-3"></i>
              <span>AI 生成内容可能存在错误,请结合项目文档核对关键信息</span>
            </div>
          </div>
        </div>
      </main>
    </div>
  `,
  setup() {
    const route = useRoute();
    const router = useRouter();

    const agents = ref([]);
    const sessions = ref([]);
    const messages = ref([]);
    const input = ref('');
    const currentSession = ref('');
    const agentId = ref('');
    const streaming = ref(false);
    const thinking = ref(false);
    const error = ref('');
    const messagesContainer = ref(null);
    const inputEl = ref(null);
    let autoSent = false;
    let isMounted = false;

    const agentPickerOpen = ref(false);
    // DeepSeek 风格：可折叠侧栏 + 模式开关 + 思考过程折叠
    const sidebarCollapsed = ref(false);
    const deepThink = ref(true);
    const kbInject = ref(true);
    const thinkingOpen = reactive({});
    const userMenuOpen = ref(false);
    let thinkingStartTime = 0;

    /* ---------- 模板用的派生数据 ---------- */
    // 当前 Agent 的 lucide 图标名(根据 title 简单匹配,后端如果有 icon 字段优先用)
    const currentAgentIcon = computed(() => agentIconByTitle(currentAgent.value && currentAgent.value.title));

    // 当前 Agent 的能力列表(从 agent.capabilities 读,空数组给默认值)
    const agentCapabilities = computed(() => {
      const caps = (currentAgent.value && currentAgent.value.capabilities) || [];
      if (Array.isArray(caps) && caps.length > 0) return caps;
      // 默认展示的通用能力
      return ['对话与问答', '多轮上下文', '代码相关问答'];
    });

    // 空状态快速入口
    const quickChips = ref([
      { title: '帮我写代码',   icon: 'code-2',     desc: '描述需求,生成可用的代码片段',          prompt: '请帮我写一段代码,需求如下:' },
      { title: '排查 Bug',     icon: 'bug',         desc: '粘贴报错或异常,我帮你定位',            prompt: '我遇到一个 Bug,报错信息如下:' },
      { title: '重构建议',     icon: 'git-compare', desc: '上传代码片段,给出重构方案',            prompt: '请帮我重构这段代码,目标是更清晰、可维护:' },
      { title: '解释概念',     icon: 'book-open',   desc: '讲清楚一个技术点的工作原理',            prompt: '请用通俗易懂的方式解释一下:' }
    ]);

    // 把消息时间规整成 HH:MM(template 用)
    function formatTime(s) {
      if (!s) return '';
      return fmtTime(s);
    }

    // 操作栏方法
    function copyMessage(content) {
      if (!content) return;
      return copyToClipboard(content);
    }

    function regenerate(i) {
      // 简单实现:找到上一条 human 消息,把它重新塞进 input 并触发 send
      if (streaming.value) return;
      const m = messages.value[i];
      if (!m || m.role !== 'ai') return;
      // 找前一条 human
      for (let j = i - 1; j >= 0; j--) {
        if (messages.value[j].role === 'human') {
          // 删除当前 ai 及之后所有消息
          messages.value.splice(j + 1);
          input.value = messages.value[j].content;
          send();
          return;
        }
      }
      if (typeof window.showToast === 'function') window.showToast('找不到要重生成的用户消息');
    }

    function likeMessage(i) {
      if (typeof window.showToast === 'function') window.showToast('已记录反馈');
      // TODO: 后端有反馈接口再接
    }

    const email = computed(() => localStorage.getItem('email') || '');
    const userInitial = computed(() => {
      const e = email.value;
      return e ? e.charAt(0).toUpperCase() : 'U';
    });

    function goUser() {
      userMenuOpen.value = false;
      router.push('/user');
    }

    function logout() {
      localStorage.removeItem('token');
      localStorage.removeItem('user_id');
      localStorage.removeItem('email');
      window.location.replace('/login');
    }

    const sendDisabled = computed(() => !input.value || !input.value.trim());

    const currentAgentName = computed(() => {
      const a = agents.value.find(x => x.id === agentId.value);
      return a ? a.title : '';
    });

    const currentAgent = computed(() => {
      const a = agents.value.find(x => x.id === agentId.value);
      return a || {};
    });

    function switchAgent(id) {
      if (!id || id === agentId.value) {
        agentPickerOpen.value = false;
        return;
      }
      agentId.value = id;
      currentSession.value = '';
      clearMessages('switchAgent');
      error.value = '';
      agentPickerOpen.value = false;
      router.replace({ path: '/chat', query: Object.assign({}, route.query, { agent_id: id, session: undefined }) });
      loadSessions();
    }

    function onDocClick() {
      agentPickerOpen.value = false;
    }

    async function loadAgents() {
      try {
        const r = await authFetch(API_BASE + '/agents');
        const d = await r.json();
        agents.value = (d.data && d.data.agents) || [];
      } catch (e) {}
    }

    async function loadSessions() {
      // 没有 agentId 时,不向后台发请求(否则会拉到全量会话,混入别的 agent),直接给空列表
      if (!agentId.value) {
        sessions.value = [];
        return;
      }
      try {
        const url = API_BASE + '/sessions?agent_id=' + agentId.value;
        const r = await authFetch(url);
        const d = await r.json();
        sessions.value = (d.data && d.data.sessions) || [];
      } catch (e) {}
    }

    // 给每次 selectSession 一个递增 token,只让最后一次的 fetch 结果生效,
    // 避免快速切换会话时,先发后回的请求把后发先回的结果覆盖掉。
    let selectSessionToken = 0;

    // 集中所有清空 messages 的入口,带 stack trace,定位"一闪就消失"的元凶
    function clearMessages(reason) {
      console.log('[clearMessages] reason=', reason, 'prev length=', messages.value.length);
      console.trace('[clearMessages] stack');
      messages.value = [];
    }

    async function selectSession(sid) {
      if (!sid) return;
      const myToken = ++selectSessionToken;
      const url = API_BASE + '/chat/' + sid;
      console.log('[selectSession] start sid=', sid, 'token=', myToken, 'url=', url);
      currentSession.value = sid;
      error.value = '';
      // 关键:不要在这里清空 messages。
      // 等 fetch 成功再赋值,失败则保留旧数据,避免"一闪就消失"。
      try {
        const r = await authFetch(url);
        console.log('[selectSession] resp status=', r.status, 'token=', myToken);
        if (myToken !== selectSessionToken) {
          console.log('[selectSession] stale, ignored', 'token=', myToken);
          return;
        }
        if (!r.ok) {
          try {
            const d = await r.json();
            error.value = d.message || '加载失败';
          } catch (_) { error.value = '加载失败 (' + r.status + ')'; }
          return;
        }
        const d = await r.json();
        if (d.code === 0 && d.data && Array.isArray(d.data.messages)) {
          const filtered = d.data.messages.filter(m =>
            m.role === 'human' || m.role === 'user' || m.role === 'ai'
          );
          console.log('[selectSession] raw.count=', d.data.messages.length,
            'filtered.count=', filtered.length, 'token=', myToken);
          console.log('[selectSession] roles preview=',
            d.data.messages.map(m => m.role).slice(0, 10).join(','));
          if (filtered.length > 0) {
            console.log('[selectSession] first filtered msg=',
              { role: filtered[0].role, content: String(filtered[0].content).slice(0, 80) });
          }
          messages.value = filtered.map(m => ({
            role: (m.role === 'human' || m.role === 'user') ? 'human' : 'ai',
            content: m.content || '',
            streaming: false
          }));
          console.log('[selectSession] messages set, length=', messages.value.length, 'token=', myToken);
          // dump 每条消息的 role + content 前 100 字,帮老板定位内容/样式问题
          messages.value.forEach((m, idx) => {
            const preview = String(m.content).slice(0, 100).replace(/\n/g, '\\n');
            console.log(`[selectSession] msg[${idx}] role=${m.role} content="${preview}"`);
          });
          // DOM 真的渲染了吗?
          nextTick(() => {
            const dom = document.querySelectorAll('.chat-messages .msg');
            console.log('[selectSession] DOM .msg count=', dom.length, 'token=', myToken);
            // 重新渲染 lucide 图标(每条消息/avatar 里都有 data-lucide)
            loadIcons();
            // 自动滚到底
            scrollToBottom();
            if (dom.length > 0) {
              const first = dom[0];
              const rect = first.getBoundingClientRect();
              const cs = window.getComputedStyle(first);
              console.log('[selectSession] first .msg rect=', {
                top: rect.top, left: rect.left, width: rect.width, height: rect.height
              }, 'opacity=', cs.opacity, 'display=', cs.display, 'visibility=', cs.visibility);
            } else {
              // 兜底:.msg 节点为 0 时,把整个 chat-messages-inner 的 innerHTML 打出来,
              // 看 v-for 到底渲染了什么,以及容器本身是否存在
              const inner = document.querySelector('.chat-messages-inner');
              const container = document.querySelector('.chat-messages');
              console.warn('[selectSession] .msg count=0, dump container:');
              console.log('  .chat-messages exists?', !!container,
                container ? 'rect=' + JSON.stringify(container.getBoundingClientRect()) : '');
              console.log('  .chat-messages-inner exists?', !!inner,
                inner ? 'rect=' + JSON.stringify(inner.getBoundingClientRect()) : '');
              console.log('  .chat-messages-inner innerHTML (first 500 chars)=',
                inner ? inner.innerHTML.slice(0, 500) : 'NULL');
            }
          });
        } else {
          console.warn('[selectSession] unexpected response', d, 'token=', myToken);
        }
      } catch (e) {
        if (myToken === selectSessionToken) {
          console.error('[selectSession] error', e, 'token=', myToken);
          error.value = '网络错误';
        }
      }
    }

    async function newSession() {
      if (streaming.value) return;
      // 没有 agentId 时,创建的是孤儿会话(后端不绑定 agent),这里挡掉
      if (!agentId.value) {
        showToast('请先选择 Agent');
        return;
      }
      try {
        const r = await authFetch(API_BASE + '/sessions', {
          method: 'POST',
          body: JSON.stringify({ agent_id: agentId.value })
        });
        const d = await r.json();
        if (d.code === 0 && d.data && d.data.session_id) {
          currentSession.value = d.data.session_id;
          clearMessages('newSession');
          error.value = '';
          sessions.value.unshift({
            id: d.data.session_id,
            title: '新会话',
            agent_id: agentId.value
          });
        }
      } catch (e) {}
    }

    async function deleteSession(s) {
      if (streaming.value) return;
      if (!confirm('确认删除此会话？')) return;
      try {
        const r = await authFetch(API_BASE + '/chat/' + s.id, { method: 'DELETE' });
        const d = await r.json();
        if (d.code === 0) {
          sessions.value = sessions.value.filter(x => x.id !== s.id);
          if (currentSession.value === s.id) {
            currentSession.value = '';
            clearMessages('deleteSession current');
          }
        }
      } catch (e) {}
    }

    function scrollToBottom() {
      nextTick(() => {
        const c = messagesContainer.value;
        if (c) c.scrollTop = c.scrollHeight;
      });
    }

    function onInput(e) {
      autoResize(e.target, 264);
    }

    function onKey(e) {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        send();
      }
    }

    function toggleThinking(i) {
      thinkingOpen[i] = !thinkingOpen[i];
    }

    async function send() {
      const msg = input.value.trim();
      if (!msg || streaming.value) return;

      input.value = '';
      if (inputEl.value) {
        inputEl.value.style.height = 'auto';
      }

      messages.value.push({ role: 'human', content: msg, streaming: false });
      messages.value.push({ role: 'ai', content: '', streaming: true, thinking: '', thinkingDuration: 0 });
      const aiIdx = messages.value.length - 1;
      thinkingStartTime = 0;

      streaming.value = true;
      thinking.value = true;
      error.value = '';
      scrollToBottom();

      const sessionId = currentSession.value || '';
      const aid = agentId.value || '';
      let newSessionCreated = !sessionId;

      try {
        const resp = await authFetch(API_BASE + '/chat/completion', {
          method: 'POST',
          body: JSON.stringify({ session_id: sessionId, message: msg, agent_id: aid })
        });

        if (!resp.ok) {
          let errMsg = '请求失败: ' + resp.status;
          try {
            const d = await resp.json();
            if (d.message) errMsg = d.message;
          } catch (e) {}
          throw new Error(errMsg);
        }

        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        let eventType = '';
        let dataStr = '';

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });

          const lines = buffer.split('\n');
          buffer = lines.pop() || '';

          for (const line of lines) {
            if (line.startsWith('event: ')) {
              eventType = line.slice(7).trim();
            } else if (line.startsWith('data: ')) {
              dataStr = line.slice(6);
            } else if (line === '' && eventType) {
              let data;
              try {
                data = JSON.parse(dataStr);
              } catch (e) {
                data = dataStr;
              }

              if (eventType === 'session') {
                const sid = typeof data === 'object' ? data.session_id : data;
                if (sid && !currentSession.value) {
                  currentSession.value = sid;
                  newSessionCreated = true;
                }
              } else if (eventType === 'thinking') {
                thinking.value = true;
                if (!thinkingStartTime) thinkingStartTime = Date.now();
                // 若后端推送思考内容（data.content），累加到当前 ai 消息
                if (deepThink.value && data && typeof data === 'object' && data.content) {
                  messages.value[aiIdx].thinking += data.content;
                  scrollToBottom();
                }
              } else if (eventType === 'chunk') {
                thinking.value = false;
                // 思考结束，记录耗时
                if (thinkingStartTime && !messages.value[aiIdx].thinkingDuration) {
                  messages.value[aiIdx].thinkingDuration = ((Date.now() - thinkingStartTime) / 1000).toFixed(1);
                }
                if (data && data.content) {
                  messages.value[aiIdx].content += data.content;
                  scrollToBottom();
                }
              } else if (eventType === 'done') {
                messages.value[aiIdx].streaming = false;
                thinking.value = false;
                if (thinkingStartTime && !messages.value[aiIdx].thinkingDuration) {
                  messages.value[aiIdx].thinkingDuration = ((Date.now() - thinkingStartTime) / 1000).toFixed(1);
                }
              } else if (eventType === 'error') {
                const errMsg = (typeof data === 'object' ? data.message : data) || '生成失败';
                error.value = errMsg;
                messages.value[aiIdx].streaming = false;
                thinking.value = false;
              }

              eventType = '';
              dataStr = '';
            }
          }
        }

        messages.value[aiIdx].streaming = false;
        thinking.value = false;

        if (newSessionCreated) {
          await loadSessions();
        }
      } catch (e) {
        if (e.message !== 'unauthorized') {
          error.value = e.message || '发送失败';
        }
        messages.value[aiIdx].streaming = false;
        thinking.value = false;
      } finally {
        streaming.value = false;
      }
    }

    onMounted(async () => {
      try {
        // 先加载 agents,确保后续 agentId 解析时 agents 已就绪,
        // 避免首次渲染时 currentAgentName 走到空 fallback。
        await loadAgents();
        const qid = route.query.agent_id || '';
        if (qid && agents.value.some(a => a.id === qid)) {
          agentId.value = qid;
        } else if (agents.value.length > 0) {
          agentId.value = agents.value[0].id;
        }
        await loadSessions();

        if (route.query.session) {
          await selectSession(route.query.session);
        }

        if (route.query.msg && !autoSent && !currentSession.value) {
          autoSent = true;
          input.value = route.query.msg;
          await send();
        } else {
          // 兜底:长消息从 sessionStorage 取(AgentsPage hero 写入)
          try {
            const pending = sessionStorage.getItem('__pending_msg');
            if (pending && !autoSent && !currentSession.value) {
              autoSent = true;
              sessionStorage.removeItem('__pending_msg');
              input.value = pending;
              await send();
            }
          } catch (e) {}
        }
      } catch (e) {
        error.value = e.message || '页面初始化失败';
      } finally {
        document.addEventListener('click', onDocClick);
        isMounted = true;
        nextTick(() => {
          // 关键:首次挂载后,把 template 里所有 <i data-lucide="..."> 转成 svg
          loadIcons();
          if (inputEl.value) inputEl.value.focus();
        });
      }
    });

    onUnmounted(() => {
      document.removeEventListener('click', onDocClick);
    });

    watch(() => route.query.agent_id, (newId, oldId) => {
      console.log('[watch agent_id] fire', { newId, oldId, isMounted, currentAgentId: agentId.value });
      if (!isMounted) return;
      if (newId === oldId) return;
      if (newId && newId !== agentId.value) {
        agentId.value = newId;
        currentSession.value = '';
        clearMessages('watch agent_id changed');
        error.value = '';
        loadSessions();
      }
    });

    // 监控 messages 变化,定位何时被清空,并在每次变更后刷新 lucide 图标
    watch(() => messages.value.length, (newLen, oldLen) => {
      console.log('[messages.length]', oldLen, '->', newLen);
      nextTick(() => loadIcons());
    });

    // sessions 列表变化(新建/删除会话)也要刷新图标
    watch(() => sessions.value.length, (newLen, oldLen) => {
      if (newLen === oldLen) return;
      nextTick(() => loadIcons());
    });

    return {
      agents, sessions, messages, input, currentSession, agentId,
      streaming, thinking, error, messagesContainer, inputEl,
      currentAgentName, currentAgent, userInitial,
      email, userMenuOpen, goUser, logout,
      newSession, selectSession, deleteSession, send, onKey, onInput,
      renderMarkdown,
      agentPickerOpen, switchAgent,
      sidebarCollapsed, deepThink, kbInject, thinkingOpen, toggleThinking,
      // 新版模板新增的字段
      currentAgentIcon, agentCapabilities, quickChips,
      copyMessage, regenerate, likeMessage, formatTime, fmtTime
    };
  }
};

/* ============================================================
   UserPage
   ============================================================ */
const UserPage = {
  template: `
    <div class="user-layout">
      <!-- 左栏：用户信息 + Tab导航 -->
      <aside class="user-nav">
        <div class="user-profile-card">
          <div class="user-profile-avatar">{{ userInitial }}</div>
          <div class="user-profile-name">{{ displayEmail || '未登录用户' }}</div>
          <div class="user-profile-role">AI Agent 用户</div>
        </div>
        <div class="user-nav-menu">
          <button class="nav-item" :class="{ active: tab === 'chatModel' }" @click="tab = 'chatModel'">
            <span class="nav-icon">💬</span>
            <span>对话模型</span>
          </button>
          <button class="nav-item" :class="{ active: tab === 'embModel' }" @click="tab = 'embModel'">
            <span class="nav-icon">🧠</span>
            <span>Embedding 模型</span>
          </button>
          <button class="nav-item" :class="{ active: tab === 'kb' }" @click="tab = 'kb'">
            <span class="nav-icon">📚</span>
            <span>知识库设置</span>
          </button>
          <button class="nav-item" :class="{ active: tab === 'profile' }" @click="tab = 'profile'">
            <span class="nav-icon">👤</span>
            <span>个人资料</span>
          </button>
          <button class="nav-item" :class="{ active: tab === 'about' }" @click="tab = 'about'">
            <span class="nav-icon">ℹ️</span>
            <span>关于</span>
          </button>
        </div>
      </aside>

      <!-- 中栏：配置表单 -->
      <div class="user-form-area">
        <div class="user-form-header">
          <h2 class="form-page-title">{{ currentTabTitle }}</h2>
          <span class="form-page-subtitle" v-if="tab !== 'about' && tab !== 'profile'">模型配置将用于所有 Agent 的对话与 RAG 检索</span>
          <span class="form-page-subtitle" v-else-if="tab === 'profile'">编辑个人资料与偏好</span>
          <span class="form-page-subtitle" v-else>平台信息</span>
        </div>

        <!-- 对话模型 -->
        <div class="panel" v-show="tab === 'chatModel'">
          <div class="panel-icon-label"><span class="panel-icon">💬</span><span>对话模型配置</span></div>
          <div class="form-group">
            <label>Model 名称</label>
            <input class="input" v-model="chat.model" placeholder="例如: deepseek-chat、gpt-4o">
            <div class="hint">用于 Agent 对话推理的 LLM 模型标识</div>
          </div>
          <div class="form-group">
            <label>Base URL</label>
            <input class="input" v-model="chat.base_url" placeholder="例如: https://api.deepseek.com/v1">
            <div class="hint">模型服务的 API 根地址，如使用 OpenAI 兼容协议可填对应域名</div>
          </div>
          <div class="form-group">
            <label>API Token</label>
            <input class="input" type="password" v-model="chat.token" placeholder="sk-xxxxxxxx">
            <div class="hint">API 访问密钥，仅本地保存与提交</div>
          </div>
          <div class="form-actions">
            <button class="btn btn-primary" @click="saveModel(1)" :disabled="saving === 1">
              {{ saving === 1 ? '保存中...' : '保存对话模型' }}
            </button>
          </div>
        </div>

        <!-- Embedding 模型 -->
        <div class="panel" v-show="tab === 'embModel'">
          <div class="panel-icon-label"><span class="panel-icon">🧠</span><span>Embedding 模型配置</span></div>
          <div class="form-group">
            <label>Model 名称</label>
            <input class="input" v-model="embedding.model" placeholder="例如: text-embedding-v3">
            <div class="hint">用于知识库文档向量化的 Embedding 模型</div>
          </div>
          <div class="form-group">
            <label>Base URL</label>
            <input class="input" v-model="embedding.base_url" placeholder="例如: https://api.deepseek.com/v1">
            <div class="hint">Embedding 服务的 API 根地址</div>
          </div>
          <div class="form-group">
            <label>API Token</label>
            <input class="input" type="password" v-model="embedding.token" placeholder="sk-xxxxxxxx">
            <div class="hint">API 访问密钥</div>
          </div>
          <div class="form-actions">
            <button class="btn btn-primary" @click="saveModel(2)" :disabled="saving === 2">
              {{ saving === 2 ? '保存中...' : '保存 Embedding 模型' }}
            </button>
          </div>
        </div>

        <!-- 知识库设置 -->
        <div class="panel" v-show="tab === 'kb'">
          <div class="panel-icon-label"><span class="panel-icon">📚</span><span>知识库全局设置</span></div>
          <div class="kb-settings-grid">
            <div class="form-group">
              <label>默认分块大小（字）</label>
              <input class="input" v-model="kb.chunkSize" type="number" min="100" max="4000">
              <div class="hint">文档上传时，每块文本的默认长度</div>
            </div>
            <div class="form-group">
              <label>重叠字符数</label>
              <input class="input" v-model="kb.chunkOverlap" type="number" min="0" max="1000">
              <div class="hint">相邻分块的重叠字符，保持语义连续性</div>
            </div>
            <div class="form-group">
              <label>Top-K 检索数量</label>
              <input class="input" v-model="kb.topK" type="number" min="1" max="50">
              <div class="hint">RAG 检索时返回的最多分块数</div>
            </div>
            <div class="form-group">
              <label>相似度阈值</label>
              <input class="input" v-model="kb.threshold" type="number" step="0.01" min="0" max="1">
              <div class="hint">低于此分数的分块将被过滤（0 = 不过滤）</div>
            </div>
          </div>
          <div class="form-group form-group-checkbox">
            <label class="check-label">
              <input type="checkbox" v-model="kb.autoIndex">
              <span>上传后自动索引</span>
            </label>
            <div class="hint">文档上传完成后立即生成向量</div>
          </div>
          <div class="form-group form-group-checkbox">
            <label class="check-label">
              <input type="checkbox" v-model="kb.showCitations">
              <span>对话中显示知识库来源</span>
            </label>
            <div class="hint">AI 回答中标注引用的知识库文档</div>
          </div>
          <div class="form-actions">
            <button class="btn btn-primary" @click="saveKb">保存知识库设置</button>
          </div>
        </div>

        <!-- 个人资料 -->
        <div class="panel" v-show="tab === 'profile'">
          <div class="panel-icon-label"><span class="panel-icon">👤</span><span>个人资料</span></div>
          <div class="form-group">
            <label>邮箱</label>
            <input class="input" :value="displayEmail" disabled>
            <div class="hint">登录账户邮箱</div>
          </div>
          <div class="form-group">
            <label>昵称</label>
            <input class="input" v-model="profile.nickname" placeholder="你的昵称">
            <div class="hint">用于对话与智能体引用你时的称呼</div>
          </div>
          <div class="form-group">
            <label>界面主题</label>
            <select class="input" v-model="profile.theme">
              <option value="aurora">Aurora Glass（冷黑蓝×青紫极光）</option>
              <option value="warm">紫金暖调</option>
            </select>
            <div class="hint">界面配色风格</div>
          </div>
          <div class="form-group">
            <label>默认智能体</label>
            <select class="input" v-model="profile.defaultAgentId">
              <option value="">—— 自动选择第一个 ——</option>
              <option v-for="a in agentOptions" :key="a.id" :value="a.id">{{ a.title }}</option>
            </select>
            <div class="hint">进入对话页默认使用的 Agent</div>
          </div>
          <div class="form-actions">
            <button class="btn btn-primary" @click="saveProfile">保存资料</button>
          </div>
        </div>

        <!-- 关于 -->
        <div class="panel" v-show="tab === 'about'">
          <div class="panel-icon-label"><span class="panel-icon">ℹ️</span><span>关于 AI Agent</span></div>
          <div class="about-grid">
            <div class="about-item"><span class="about-k">版本</span><span class="about-v">v1.0.0</span></div>
            <div class="about-item"><span class="about-k">前端</span><span class="about-v">Vue 3 CDN + Tailwind v4</span></div>
            <div class="about-item"><span class="about-k">后端</span><span class="about-v">Gin + LangChainGo</span></div>
            <div class="about-item"><span class="about-k">向量库</span><span class="about-v">Memory / Milvus / Redis</span></div>
            <div class="about-item"><span class="about-k">RAG</span><span class="about-v">混合检索 (向量 + BM25) + RRF</span></div>
            <div class="about-item"><span class="about-k">分块策略</span><span class="about-v">中文递归分割 · 重叠窗口</span></div>
          </div>
          <p class="about-desc">
            本平台支持自定义 Agent、会话管理、多模型提供商、知识库文档向量化与混合检索、工具调用等功能。
          </p>
        </div>
      </div>

      <!-- 右栏：状态预览 -->
      <aside class="user-preview">
        <div class="preview-card">
          <div class="preview-card-title">🔌 连接状态</div>
          <div class="status-row">
            <span class="status-label">对话模型</span>
            <span class="status-dot" :class="{ ok: chat.model && chat.token, warn: !chat.model || !chat.token }"></span>
            <span class="status-val">{{ chat.model ? '已配置' : '未配置' }}</span>
          </div>
          <div class="status-row">
            <span class="status-label">Embedding</span>
            <span class="status-dot" :class="{ ok: embedding.model && embedding.token, warn: !embedding.model || !embedding.token }"></span>
            <span class="status-val">{{ embedding.model ? '已配置' : '未配置' }}</span>
          </div>
          <div class="status-row">
            <span class="status-label">知识库</span>
            <span class="status-dot ok"></span>
            <span class="status-val">就绪</span>
          </div>
        </div>
        <div class="preview-card">
          <div class="preview-card-title">📊 配置摘要</div>
          <div class="summary-row" v-if="chat.model">
            <span class="summary-k">Chat Model</span>
            <span class="summary-v">{{ chat.model }}</span>
          </div>
          <div class="summary-row" v-if="chat.base_url">
            <span class="summary-k">Base URL</span>
            <span class="summary-v summary-truncate" :title="chat.base_url">{{ chat.base_url }}</span>
          </div>
          <div class="summary-row" v-if="embedding.model">
            <span class="summary-k">Embedding</span>
            <span class="summary-v">{{ embedding.model }}</span>
          </div>
          <div class="summary-row">
            <span class="summary-k">分块大小</span>
            <span class="summary-v">{{ kb.chunkSize }} 字</span>
          </div>
          <div class="summary-row">
            <span class="summary-k">Top-K</span>
            <span class="summary-v">{{ kb.topK }}</span>
          </div>
          <div class="summary-row" v-if="profile.nickname">
            <span class="summary-k">昵称</span>
            <span class="summary-v">{{ profile.nickname }}</span>
          </div>
        </div>
      </aside>
    </div>
  `,
  setup() {
    const tab = ref('chatModel');
    const saving = ref(0);
    const chat = reactive({ model: '', base_url: '', token: '' });
    const embedding = reactive({ model: '', base_url: '', token: '' });
    const kb = reactive({ chunkSize: 500, chunkOverlap: 50, topK: 5, threshold: 0, autoIndex: true, showCitations: true });
    const profile = reactive({ nickname: '', theme: 'aurora', defaultAgentId: '' });
    const agentOptions = ref([]);
    const displayEmail = computed(() => localStorage.getItem('email') || '');
    const userInitial = computed(() => {
      const e = displayEmail.value;
      return e ? e.charAt(0).toUpperCase() : 'U';
    });
    const tabTitles = {
      chatModel: '对话模型',
      embModel: 'Embedding 模型',
      kb: '知识库设置',
      profile: '个人资料',
      about: '关于',
    };
    const currentTabTitle = computed(() => tabTitles[tab.value] || '设置');

    async function loadConfig() {
      try {
        const r = await authFetch(API_BASE + '/user/model');
        const d = await r.json();
        if (d.data) {
          if (d.data.chat) {
            chat.model = d.data.chat.model || '';
            chat.base_url = d.data.chat.base_url || '';
            chat.token = d.data.chat.token || '';
          }
          if (d.data.embedding) {
            embedding.model = d.data.embedding.model || '';
            embedding.base_url = d.data.embedding.base_url || '';
            embedding.token = d.data.embedding.token || '';
          }
        }
      } catch (e) {}
    }

    async function loadAgents() {
      try {
        const r = await authFetch(API_BASE + '/agents');
        const d = await r.json();
        agentOptions.value = (d.data && d.data.agents) || [];
      } catch (e) {}
    }

    async function saveModel(modelType) {
      const cfg = modelType === 1 ? chat : embedding;
      const model = (cfg.model || '').trim();
      const token = (cfg.token || '').trim();
      if (!model || !token) { showToast('请填写 Model 和 Token'); return; }
      saving.value = modelType;
      try {
        const r = await authFetch(API_BASE + '/user/model', {
          method: 'POST',
          body: JSON.stringify({
            model_type: modelType,
            model: model,
            base_url: (cfg.base_url || '').trim(),
            token: token
          })
        });
        const d = await r.json();
        if (d.code === 0) {
          showToast('保存成功');
          await loadConfig();
        } else {
          showToast(d.message || '保存失败');
        }
      } catch (e) {
        showToast('网络错误');
      } finally {
        saving.value = 0;
      }
    }

    function saveKb() {
      showToast('知识库设置已保存（默认值）');
    }
    function saveProfile() {
      showToast('个人资料已保存');
    }

    onMounted(() => {
      loadConfig();
      loadAgents();
    });

    return {
      tab, saving, chat, embedding, kb, profile, agentOptions,
      displayEmail, userInitial, currentTabTitle,
      saveModel, saveKb, saveProfile,
    };
  }
};

/* ============================================================
   App Root
   ============================================================ */
const App = {
  template: `
    <div class="app-bg"></div>
    <template v-if="!isLogin && !isChat">
      <div class="topbar">
        <div class="topbar-brand" @click="$router.push('/agents')" style="cursor:pointer;">
          <div class="logo">A</div>
          <span>ansmeee</span>
          <span class="ver">v1.0</span>
        </div>
        <div class="topbar-actions">
          <div id="topbar-chat-slot"></div>
          <div class="user-menu" v-if="email">
            <div class="user-avatar" @click.stop="showUserMenu = !showUserMenu">{{ userInitial }}</div>
            <div class="user-dropdown" v-if="showUserMenu" @click.stop>
              <a @click="goUser">个人中心</a>
              <div class="logout" @click="logout">退出登录</div>
            </div>
          </div>
        </div>
      </div>
    </template>
    <router-view v-slot="{ Component }">
      <transition name="fade" mode="out-in">
        <component :is="Component" />
      </transition>
    </router-view>
    <div class="toast" :class="{ show: toastState.show }">{{ toastState.msg }}</div>
  `,
  setup() {
    const route = useRoute();
    const router = useRouter();
    const showUserMenu = ref(false);

    const isLogin = computed(() => route.path === '/login');
    const isChat = computed(() => route.path === '/chat');
    const email = computed(() => localStorage.getItem('email') || '');
    const userInitial = computed(() => {
      const e = email.value;
      return e ? e.charAt(0).toUpperCase() : 'U';
    });

    function goUser() {
      showUserMenu.value = false;
      router.push('/user');
    }

    function logout() {
      localStorage.removeItem('token');
      localStorage.removeItem('user_id');
      localStorage.removeItem('email');
      window.location.replace('/login');
    }

    function onDocClick() {
      showUserMenu.value = false;
    }

    onMounted(() => {
      document.addEventListener('click', onDocClick);
    });
    onUnmounted(() => {
      document.removeEventListener('click', onDocClick);
    });

    return { isLogin, isChat, email, userInitial, showUserMenu, goUser, logout, toastState };
  }
};

/* ============================================================
   Router
   ============================================================ */
const routes = [
  { path: '/', redirect: '/agents' },
  { path: '/login', component: LoginPage },
  { path: '/agents', component: AgentsPage },
  { path: '/agent', component: AgentDetailPage },
  { path: '/chat', component: ChatPage },
  { path: '/user', component: UserPage }
];

const router = createRouter({
  history: createWebHistory(),
  routes
});

router.beforeEach((to, from, next) => {
  if (to.path !== '/login' && !getToken()) {
    next('/login');
  } else {
    next();
  }
});

/* ============================================================
   Mount
   ============================================================ */
const app = createApp(App);
app.use(router);
router.isReady().then(() => app.mount('#app'));
