/* deepx web dashboard — Vue 3 global build, 零构建。
   通过 SSE 镜像终端会话,POST 回注输入 / review 确认。 */
const { createApp } = Vue;

marked.setOptions({ breaks: true, gfm: true });

// 跟 TUI 同步的 web UI 文案。lang 由后端快照 / lang 事件同步(读 ~/.deepx/meta.json)。
const I18N = {
  zh: {
    connected: '已连接',
    reconnecting: '重连中…',
    streaming: '生成中…',
    github: '在 GitHub 上查看本项目',
    stop: '停止',
    you: '你',
    send: '发送',
    'placeholder.idle': '输入消息,Enter 发送,Shift+Enter 换行',
    'placeholder.streaming': '正在生成…(可继续输入,发送后排队)',
    'queued.title': '待发送(本轮结束后自动发出)',
    'review.title': '需要确认',
    'review.approve': '批准',
    'review.reject': '拒绝',
    'ask.title': '请选择',
    'ask.single': '(单选)',
    'ask.multi': '(多选)',
    'ask.submit': '提交',
    'ask.cancel': '取消',
    'ask.skipped': '（已跳过）',
    'ask.none': '（未选）',
    'ask.join': '、',
    'footer.thinking': '思考中',
    'footer.streaming': '输出中',
    'footer.tool': '调用工具',
    'footer.error': '出错',
    'done.done': '完成',
    'done.tools': '次工具调用',
    welcome: '你好,我是 deepx-code 👋 描述你的需求,我来帮你读代码、改代码、跑命令。可用 @ 引用工作区文件,Enter 发送。',
    'act.compact': '压缩会话',
    'act.mcp': 'MCP 管理',
    'act.skill': 'Skill 管理',
    'mgr.none': '暂无',
    'mgr.add': '添加',
    'mgr.delete': '删除',
    'mgr.close': '关闭',
    'mcp.name': '名称',
    'mcp.command': '命令(stdio,如 npx)',
    'mcp.args': '参数(空格分隔)',
    'mcp.or': '或',
    'mcp.url': 'HTTP URL',
    'skill.add': '从 GitHub URL / 本地路径安装',
    'skill.src': 'https://github.com/owner/repo 或本地路径',
    'skill.installing': '安装中…',
    'skill.builtin': '内置',
    'skill.tab.install': '安装',
    'skill.tab.installed': '已安装',
    'skill.search.title': '从 Clawhub 搜索',
    'skill.search.ph': '搜索 skill 关键词',
    'skill.search.btn': '搜索',
    'skill.searching': '搜索中…',
    'skill.noresult': '无结果',
    workspace: '工作区',
    'panel.vendor': '模型厂商',
    'panel.endpoint': '接口',
    'panel.balance': '余额',
    'panel.curmodel': '当前模型',
    'panel.context': '上下文',
    'pane.hide_sessions': '收起会话栏',
    'pane.show_sessions': '展开会话栏',
    'pane.hide_status': '收起状态栏',
    'pane.show_status': '展开状态栏',
    'panel.todo': '待办',
    'panel.plan': '计划',
    'panel.tools': '工具调用',
    'label.used': '占用',
    'label.output': '输出',
    'label.cache': '缓存',
    'session.new': '＋ 新建会话',
    'session.untitled': '未命名',
    'session.rename': '重命名',
    'session.delete': '删除',
    'session.rename.prompt': '输入新的会话名称:',
    'session.delete.confirm': '确定删除这个会话吗?此操作不可撤销。',
    'session.delete.default_hint': '默认会话不可删除',
    'confirm.ok': '确定',
    'confirm.cancel': '取消',
    'panel.routing': '模型路由',
    'panel.mode': '权限模式',
    'panel.sandbox': '沙箱',
    'panel.workingmode': '工作模式',
    'panel.codegraph': '代码图谱',
    'cg.idle': '—',
    'cg.loading': '加载',
    'cg.ready': '就绪',
    'cg.stale': '更新',
    'cg.disabled': '已禁用',
    'cg.degraded': '降级',
  },
  en: {
    connected: 'connected',
    reconnecting: 'reconnecting…',
    streaming: 'streaming…',
    github: 'View this project on GitHub',
    stop: 'Stop',
    you: 'You',
    send: 'Send',
    'placeholder.idle': 'Type a message — Enter to send, Shift+Enter for newline',
    'placeholder.streaming': 'Generating… (you can keep typing; it queues after send)',
    'queued.title': 'Queued (sent automatically when this turn ends)',
    'review.title': 'Confirmation needed',
    'review.approve': 'Approve',
    'review.reject': 'Reject',
    'ask.title': 'Please choose',
    'ask.single': '(single)',
    'ask.multi': '(multiple)',
    'ask.submit': 'Submit',
    'ask.cancel': 'Cancel',
    'ask.skipped': '(skipped)',
    'ask.none': '(none)',
    'ask.join': ', ',
    'footer.thinking': 'Thinking',
    'footer.streaming': 'Responding',
    'footer.tool': 'Running tool',
    'footer.error': 'Error',
    'done.done': 'Done',
    'done.tools': 'tool calls',
    welcome: "Hi, I'm deepx-code 👋 Tell me what you need — I'll read, edit and run code for you. Use @ to reference workspace files, Enter to send.",
    'act.compact': 'Compact',
    'act.mcp': 'MCP',
    'act.skill': 'Skills',
    'mgr.none': 'None',
    'mgr.add': 'Add',
    'mgr.delete': 'Delete',
    'mgr.close': 'Close',
    'mcp.name': 'Name',
    'mcp.command': 'Command (stdio, e.g. npx)',
    'mcp.args': 'Args (space-separated)',
    'mcp.or': 'or',
    'mcp.url': 'HTTP URL',
    'skill.add': 'Install from GitHub URL / local path',
    'skill.src': 'https://github.com/owner/repo or local path',
    'skill.installing': 'Installing…',
    'skill.builtin': 'built-in',
    'skill.tab.install': 'Install',
    'skill.tab.installed': 'Installed',
    'skill.search.title': 'Search Clawhub',
    'skill.search.ph': 'search skills',
    'skill.search.btn': 'Search',
    'skill.searching': 'Searching…',
    'skill.noresult': 'No results',
    workspace: 'Workspace',
    'panel.vendor': 'Vendor',
    'panel.endpoint': 'Endpoint',
    'panel.balance': 'Balance',
    'panel.curmodel': 'Model',
    'panel.context': 'Context',
    'pane.hide_sessions': 'Collapse sessions',
    'pane.show_sessions': 'Expand sessions',
    'pane.hide_status': 'Collapse status',
    'pane.show_status': 'Expand status',
    'panel.todo': 'Todo',
    'panel.plan': 'Plan',
    'panel.tools': 'Tool Calls',
    'label.used': 'Used',
    'label.output': 'Output',
    'label.cache': 'Cache',
    'session.new': '＋ New chat',
    'session.untitled': 'untitled',
    'session.rename': 'Rename',
    'session.delete': 'Delete',
    'session.rename.prompt': 'Enter a new conversation name:',
    'session.delete.confirm': 'Delete this conversation? This cannot be undone.',
    'session.delete.default_hint': 'The default conversation cannot be deleted',
    'confirm.ok': 'Confirm',
    'confirm.cancel': 'Cancel',
    'panel.routing': 'Routing',
    'panel.mode': 'Permission',
    'panel.sandbox': 'Sandbox',
    'panel.workingmode': 'Working Mode',
    'panel.codegraph': 'CodeGraph',
    'cg.idle': '—',
    'cg.loading': 'loading',
    'cg.ready': 'ready',
    'cg.stale': 'stale',
    'cg.disabled': 'disabled',
    'cg.degraded': 'degraded',
  },
};

// 控制项可选值(与 TUI / 后端一致)。
const ROUTING_OPTIONS = ['auto', 'flash', 'pro'];
const MODE_OPTIONS = ['plan', 'auto', 'review'];
const SANDBOX_OPTIONS = ['off', 'native', 'docker'];
const WORKING_MODE_OPTIONS = ['karpathy', 'openspec', 'superpowers'];

createApp({
  data() {
    return {
      messages: [],
      plan: [],
      step: [],
      toolCalls: [],
      usage: null,
      streaming: false,
      models: { flash: '', pro: '', activeRole: 'flash' },
      workspace: '',
      reviewPending: null,
      askPending: null, // ask_request 的 questions 数组,null = 无
      askSel: [],       // [题][选项] 勾选态
      queued: [],       // 流式/压缩中排队待发送的消息原文(FIFO),展示在输入框上方(issue #161)
      // 活动状态行(对齐 TUI 输入框上方那条):状态 / 实时耗时 / 工具调用
      status: 'idle',   // idle | thinking | streaming | tool | error
      showThinking: false, // /thinking 偏好,由快照 / show_thinking 事件与 TUI 同步
      openThinkingIdx: -1, // 当前正在流式的 thinking 消息下标;-1 表示没有
      turnStart: 0,     // 本轮起始时间戳(ms)
      turnElapsed: 0,   // 上一轮总耗时(ms),非 streaming 时显示
      nowTick: 0,       // 由定时器刷新,驱动 streaming 时的实时耗时
      // 左栏操作:MCP / Skill 管理弹窗
      mcpShow: false, mcpList: [], mcpForm: { name: '', command: '', args: '', url: '' },
      skillShow: false, skillTab: 'installed', skillList: [], skillForm: { src: '' },
      skillInstalling: false, skillMsg: '', skillErr: false,
      skillQuery: '', skillResults: [], skillSearching: false, skillSearched: false,
      toast: '', toastTimer: null, // 顶部临时提示(如压缩完成)
      confirmBox: null, // 自定义确认弹窗:null=关闭;{ text, danger, onYes } 时显示(不用浏览器原生 confirm)
      promptBox: null,  // 自定义输入弹窗:null=关闭;{ title, value, placeholder, onOk } 时显示(不用浏览器原生 prompt)
      input: '',
      connected: false,
      openIdx: -1, // 当前流式 assistant 消息下标
      lang: 'zh',  // 跟 TUI 同步
      // 控制态(对齐 TUI),由快照 / 增量事件同步
      vendor: '',
      routing: 'auto',
      mode: 'review',
      sandbox: 'native',
      workingMode: 'karpathy',
      codegraph: '',
      balance: '',     // 账户剩余余额展示串(¥110.00);"" 不显示,"-" 不支持
      sessions: [],
      // 两侧栏折叠态(本地持久化,刷新保留)
      sidebarCollapsed: localStorage.getItem('deepx.sidebarCollapsed') === '1',
      statusCollapsed: localStorage.getItem('deepx.statusCollapsed') === '1',
      menuOpen: null, // 当前展开"…"菜单的会话 id
      routingOptions: ROUTING_OPTIONS,
      modeOptions: MODE_OPTIONS,
      sandboxOptions: SANDBOX_OPTIONS,
      workingModeOptions: WORKING_MODE_OPTIONS,
      // @ 文件提及选择器
      mention: { active: false, idx: 0, query: '', start: 0, end: 0, hidden: false },
      mentionFiles: [],       // /api/files 拉到的工作区文件列表(懒加载、缓存)
      mentionFilesLoaded: false,
    };
  },
  computed: {
    // 已流式但还没出 token 时显示打字动画;工具执行中(已有工具条在转)不再叠一个,避免两个卡片框
    thinking() {
      return this.streaming && this.openIdx < 0 && this.status !== 'tool';
    },
    // 状态行:实时耗时(streaming 用 now-start,否则用上轮总耗时)+ 当前工具
    elapsedMs() {
      return this.streaming ? Math.max(0, this.nowTick - this.turnStart) : this.turnElapsed;
    },
    elapsedText() {
      return this.fmtElapsed(this.elapsedMs);
    },
    activeToolName() {
      for (let i = this.toolCalls.length - 1; i >= 0; i--) {
        if (this.toolCalls[i].status === 'running') return this.toolCalls[i].name;
      }
      return '';
    },
    // 状态行是否显示:运行中 / 出错 / 跑过至少一轮(对齐 TUI:启动未跑过时留空)
    showStatus() {
      return this.streaming || this.status === 'error' || this.turnElapsed > 0;
    },
    // 当前实际在用的模型名(按活跃角色取 flash / pro)
    activeModel() {
      return this.models.activeRole === 'pro' ? this.models.pro : this.models.flash;
    },
    cacheRate() {
      // 显示格式:百分比 + 原始 (hit/prompt),一眼可核对。
      // 百分比跟 TUI 一致用整数除法截断(不四舍五入)。
      if (!this.usage || !this.usage.promptTokens) return '—';
      const hit = this.usage.cacheHit;
      const prompt = this.usage.promptTokens;
      return Math.floor((hit * 100) / prompt) + '% (' + hit + '/' + prompt + ')';
    },
    // @ 选择器当前候选(按 query 过滤,最多 10 条),跟 TUI filterWorkspaceFiles 同口径。
    mentionMatches() {
      if (!this.mention.active) return [];
      return this.filterFiles(this.mention.query, this.mentionFiles, 10);
    },
  },
  methods: {
    t(key) {
      const dict = I18N[this.lang] || I18N.zh;
      return dict[key] || (I18N.zh[key] || key);
    },
    render(text) {
      try { return marked.parse(text || ''); } catch (_) { return text || ''; }
    },
    // 代码图谱状态 token → 可读标签(对齐 TUI 的 codegraph.* 文案)
    cgLabel(s) {
      return s ? (this.t('cg.' + s) || s) : '';
    },
    planIcon(s) {
      return { done: '✓', running: '▶', failed: '✗', blocked: '⏸', pending: ' ' }[s] || ' ';
    },
    planDone(list) {
      return (list || []).filter((p) => p.status === 'done').length;
    },
    toolIcon(s) {
      return { done: '✓', running: '▶', failed: '✗' }[s] || '·';
    },
    // mainArg 从工具 args JSON 里抽一个最具代表性的字段,显示在 summary 行(对齐 TUI 的
    // extractMainArg)。完整 args 仍在展开后的 <pre> 里。解析失败 / 无字段返回空串。
    mainArg(tc) {
      if (!tc.args) return '';
      let a;
      try { a = JSON.parse(tc.args); } catch (_) { return ''; }
      if (!a || typeof a !== 'object') return '';
      let v = '';
      switch (tc.name) {
        case 'Read': case 'Write': case 'Update': case 'List': case 'Tree': case 'OCR':
          v = a.path; break;
        case 'Glob':
          v = a.path && a.path !== '.' ? `${a.pattern} in ${a.path}` : a.pattern; break;
        case 'Grep':
          v = a.path ? `${a.pattern} in ${a.path}` : a.pattern; break;
        case 'Search': v = a.query; break;
        case 'Fetch': v = a.url; break;
        case 'LoadSkill': v = a.name; break;
        case 'Bash': v = a.command; break;
        case 'SwitchModel': v = a.reason; break;
        case 'Memory': v = Array.isArray(a.keywords) ? a.keywords.join(' ') : ''; break;
        case 'UpdatePlanStatus':
          v = a.id && a.status ? `${a.id} → ${a.status}` : (a.id || ''); break;
        case 'CreatePlan': v = Array.isArray(a.plans) ? `${a.plans.length} nodes` : ''; break;
        default: v = a.path || '';
      }
      v = (v == null ? '' : String(v)).replace(/\s+/g, ' ').trim();
      return v.length > 80 ? v.slice(0, 77) + '…' : v;
    },
    scrollDown(force) {
      // DOM 更新前先判断用户是否贴着底部(此刻还是旧高度):只有本来就在底部(或 force)才自动滚,
      // 否则流式中每个 token 都把用户上滚查看历史的位置反复拽回底部(issue #145:思考中滚动条动不了)。
      const el = this.$refs.msgList;
      const stick = force || !el || (el.scrollHeight - el.scrollTop - el.clientHeight < 80);
      this.$nextTick(() => {
        const e = this.$refs.msgList;
        if (e && stick) e.scrollTop = e.scrollHeight;
      });
    },
    onKey(e) {
      // @ 选择器激活时,方向键 / Tab / Enter / Esc 优先给选择器,不触发发送 / 换行。
      if (this.mention.active && this.mentionMatches.length) {
        if (e.key === 'ArrowDown') { e.preventDefault(); this.mentionMove(1); return; }
        if (e.key === 'ArrowUp') { e.preventDefault(); this.mentionMove(-1); return; }
        // Enter = 确认并关闭(文件 / 目录都选定);Tab = 对目录下钻(留在打开态)
        if (e.key === 'Enter' && !e.isComposing) { e.preventDefault(); this.mentionPick(this.mention.idx, true); return; }
        if (e.key === 'Tab') { e.preventDefault(); this.mentionPick(this.mention.idx, false); return; }
        if (e.key === 'Escape') {
          e.preventDefault(); this.mention.hidden = true; this.mention.active = false; return;
        }
      }
      if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
        e.preventDefault();
        this.send();
      }
    },

    // === @ 文件提及 ===

    // mentionContext 从光标往回扫,定位正在输入的 "@query"(镜像 TUI fileMentionContext)。
    // @ 须在串首或紧跟空白(避免 user@host 邮箱误判),且 @ 到光标间无空白。
    mentionContext(value, cursor) {
      for (let i = cursor - 1; i >= 0; i--) {
        const ch = value[i];
        if (ch === '@') {
          const prev = i > 0 ? value[i - 1] : '';
          if (i === 0 || /\s/.test(prev)) {
            return { active: true, start: i, end: cursor, query: value.slice(i + 1, cursor) };
          }
          return { active: false };
        }
        if (/\s/.test(ch)) return { active: false };
      }
      return { active: false };
    },
    filterFiles(query, files, limit) {
      const q = (query || '').toLowerCase();
      const pref = [], sub = [];
      for (const f of files) {
        const lf = f.toLowerCase();
        const base = (lf.endsWith('/') ? lf.slice(0, -1) : lf).split('/').pop();
        if (!q || lf.startsWith(q) || base.startsWith(q)) pref.push(f);
        else if (lf.includes(q)) sub.push(f);
      }
      return pref.concat(sub).slice(0, limit);
    },
    async ensureFiles() {
      if (this.mentionFilesLoaded) return;
      this.mentionFilesLoaded = true;
      try {
        const r = await fetch('/api/files');
        if (r.ok) this.mentionFiles = await r.json();
      } catch (_) { /* 拉不到就空列表,选择器不显示 */ }
    },
    // syncMention 据输入框当前值 + 光标重算提及态。keyup / input / click 时调用。
    onInput() { this.mention.hidden = false; this.syncMention(); this.autoGrow(); },
    // 输入框随内容自适应高度:先归零再按 scrollHeight 设,封顶 200px(与 CSS max-height 一致)。
    // 不做的话 textarea 固定 rows="1" 的高度,打多行也不长高(issue #145)。
    autoGrow() {
      const ta = this.$refs.ta;
      if (!ta) return;
      ta.style.height = 'auto';
      ta.style.height = Math.min(ta.scrollHeight, 200) + 'px';
    },
    // 折叠/展开左侧会话栏 / 右侧状态栏,持久化到 localStorage(刷新保留)。
    toggleSidebar() {
      this.sidebarCollapsed = !this.sidebarCollapsed;
      localStorage.setItem('deepx.sidebarCollapsed', this.sidebarCollapsed ? '1' : '0');
    },
    toggleStatus() {
      this.statusCollapsed = !this.statusCollapsed;
      localStorage.setItem('deepx.statusCollapsed', this.statusCollapsed ? '1' : '0');
    },
    syncMention() {
      const ta = this.$refs.ta;
      if (!ta) return;
      const ctx = this.mentionContext(this.input, ta.selectionStart);
      if (!ctx.active || this.mention.hidden || this.input.startsWith('/')) {
        this.mention.active = false;
        return;
      }
      this.ensureFiles();
      this.mention.start = ctx.start;
      this.mention.end = ctx.end;
      this.mention.query = ctx.query;
      this.mention.active = true;
      const n = this.mentionMatches.length;
      if (this.mention.idx >= n) this.mention.idx = Math.max(0, n - 1);
      if (this.mention.idx < 0) this.mention.idx = 0;
    },
    mentionMove(d) {
      const n = this.mentionMatches.length;
      if (!n) return;
      this.mention.idx = Math.min(n - 1, Math.max(0, this.mention.idx + d));
    },
    // mentionPick 把光标处的 "@query" 替换成 "@<相对路径>"。
    //   - commit=true(Enter / 点击):文件 / 目录都补尾随空格并关闭选择器(确认选定)
    //   - commit=false(Tab):目录不补空格、留在打开态继续下钻;文件仍补空格关闭
    // 点击走 commit —— 点哪个选哪个,符合下拉直觉;目录下钻是键盘 Tab 的高级用法(或直接打字 narrow)。
    mentionPick(i, commit) {
      const matches = this.mentionMatches;
      if (!matches.length) return;
      const idx = (typeof i === 'number') ? i : this.mention.idx;
      const chosen = matches[idx];
      const drill = !commit && chosen.endsWith('/');
      const insert = '@' + chosen + (drill ? '' : ' ');
      this.input = this.input.slice(0, this.mention.start) + insert + this.input.slice(this.mention.end);
      const caret = this.mention.start + insert.length;
      this.mention.idx = 0;
      if (!drill) this.mention.active = false;
      this.$nextTick(() => {
        const ta = this.$refs.ta;
        if (ta) { ta.focus(); ta.setSelectionRange(caret, caret); if (drill) this.syncMention(); }
      });
    },
    async send() {
      const text = this.input.trim();
      if (!text) return;
      this.input = '';
      this.mention.active = false;
      this.$nextTick(() => this.autoGrow()); // 发送后高度复位回单行
      this.scrollDown(true);                 // 自己发的消息:强制跟到底部
      await fetch('/api/input', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text }),
      }).catch(() => {});
    },
    async review(approve) {
      await fetch('/api/review', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ approve }),
      }).catch(() => {});
    },
    toggleAsk(qi, oi, multiple) {
      if (!this.askSel[qi]) return;
      if (multiple) {
        this.askSel[qi][oi] = !this.askSel[qi][oi];
      } else {
        // 单选:同题互斥
        this.askSel[qi] = this.askSel[qi].map((_, i) => i === oi);
      }
    },
    // pushAskRecord 把作答折叠成一条 ask-record 消息留在对话流(对齐 TUI 的 kindSystem 档案段,issue #134):
    // 每题一行「❓ 问题 → **已选答案**」(多选用顿号连接);skipped=true 记「（已跳过）」。
    // 必须在清空 askSel 之前调用 —— 它要读 askSel 取选中项。
    pushAskRecord(questions, skipped) {
      const lines = (questions || []).map((q, qi) => {
        if (skipped) return `❓ ${q.question} — ${this.t('ask.skipped')}`;
        const sel = (q.options || [])
          .filter((_, oi) => this.askSel[qi] && this.askSel[qi][oi])
          .map(opt => opt.label);
        const ans = sel.length ? sel.join(this.t('ask.join')) : this.t('ask.none');
        return `❓ ${q.question} → **${ans}**`;
      });
      if (lines.length) this.messages.push({ role: 'ask-record', content: lines.join('\n\n') });
    },
    async submitAsk() {
      // 组装成与终端一致的格式:{"answers":[{question, selected:[value...]}]}
      const questions = this.askPending || [];
      const answers = questions.map((q, qi) => ({
        question: q.question,
        selected: (q.options || [])
          .filter((_, oi) => this.askSel[qi] && this.askSel[qi][oi])
          .map(opt => opt.value || opt.label),
      }));
      const answer = JSON.stringify({ answers });
      this.pushAskRecord(questions, false); // 留痕:先于清空 askSel
      this.askPending = null;
      this.askSel = [];
      await fetch('/api/ask-answer', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ answer }),
      }).catch(() => {});
    },
    async cancelAsk() {
      this.pushAskRecord(this.askPending, true); // 取消也留痕:先于清空 askSel
      this.askPending = null;
      this.askSel = [];
      await fetch('/api/ask-answer', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ answer: '' }),
      }).catch(() => {});
    },
    async interrupt() {
      // 乐观地先停掉本地 streaming 态;真正中断由后端广播 interrupted 再次确认。
      this.streaming = false;
      await fetch('/api/interrupt', { method: 'POST' }).catch(() => {});
    },
    // —— 左栏操作:压缩会话 / MCP 管理 / Skill 管理 ——
    async compact() {
      if (this.streaming) return;
      await this.post('/api/compact');
    },
    async openMcp() { this.mcpShow = true; await this.loadMcp(); },
    async loadMcp() {
      try { const r = await fetch('/api/mcp-list'); this.mcpList = (await r.json()) || []; }
      catch { this.mcpList = []; }
    },
    async addMcp() {
      const f = this.mcpForm;
      if (!f.name.trim() || (!f.command.trim() && !f.url.trim())) return;
      await this.post('/api/mcp-add', {
        name: f.name.trim(), command: f.command.trim(), args: f.args.trim(), url: f.url.trim(),
      });
      this.mcpForm = { name: '', command: '', args: '', url: '' };
      setTimeout(() => this.loadMcp(), 500); // 等后台落盘 + 连接
    },
    async deleteMcp(name) {
      await this.post('/api/mcp-delete', { name });
      setTimeout(() => this.loadMcp(), 300);
    },
    showToast(text) {
      if (!text) return;
      this.toast = text;
      if (this.toastTimer) clearTimeout(this.toastTimer);
      this.toastTimer = setTimeout(() => { this.toast = ''; }, 3500);
    },
    // askConfirm 弹 app 内自定义确认框(替代浏览器原生 confirm):opts = { text, danger, onYes }。
    askConfirm(opts) { this.confirmBox = opts; },
    confirmYes() {
      const cb = this.confirmBox;
      this.confirmBox = null;
      if (cb && cb.onYes) cb.onYes();
    },
    confirmNo() { this.confirmBox = null; },
    // askPrompt 弹 app 内自定义输入框(替代浏览器原生 prompt):opts = { title, value, placeholder, onOk }。
    askPrompt(opts) {
      this.promptBox = { title: '', value: '', placeholder: '', ...opts };
      this.$nextTick(() => { if (this.$refs.promptInput) { this.$refs.promptInput.focus(); this.$refs.promptInput.select(); } });
    },
    promptOk() {
      const cb = this.promptBox;
      this.promptBox = null;
      if (cb && cb.onOk) cb.onOk((cb.value || '').trim());
    },
    promptCancel() { this.promptBox = null; },
    async openSkill() {
      this.skillShow = true; this.skillMsg = ''; this.skillTab = 'installed';
      this.skillResults = []; this.skillSearched = false; this.skillQuery = '';
      await this.loadSkill();
    },
    async searchSkill() {
      const q = this.skillQuery.trim();
      if (!q || this.skillSearching) return;
      this.skillSearching = true; this.skillMsg = '';
      try {
        const r = await fetch('/api/skill-search', {
          method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ query: q }),
        });
        const d = await r.json();
        this.skillResults = d.results || [];
        if (d.error) { this.skillMsg = '搜索失败: ' + d.error; this.skillErr = true; }
      } catch { this.skillMsg = '搜索失败'; this.skillErr = true; this.skillResults = []; }
      this.skillSearched = true; this.skillSearching = false;
    },
    async installSource(r) {
      if (this.skillInstalling) return;
      this.skillInstalling = true; this.skillMsg = '';
      try {
        const resp = await fetch('/api/skill-install-source', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ sourceId: r.sourceId, remoteRef: r.remoteRef }),
        });
        const d = await resp.json();
        if (d.ok) { this.skillMsg = '已安装: ' + (d.name || r.name); this.skillErr = false; await this.loadSkill(); }
        else { this.skillMsg = '失败: ' + (d.error || ''); this.skillErr = true; }
      } catch { this.skillMsg = '失败'; this.skillErr = true; }
      this.skillInstalling = false;
    },
    async loadSkill() {
      try { const r = await fetch('/api/skill-list'); this.skillList = (await r.json()) || []; }
      catch { this.skillList = []; }
    },
    async addSkill() {
      const src = this.skillForm.src.trim();
      if (!src || this.skillInstalling) return;
      this.skillInstalling = true; this.skillMsg = '';
      try {
        const r = await fetch('/api/skill-install', {
          method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ src }),
        });
        const d = await r.json();
        if (d.ok) { this.skillMsg = '已安装: ' + (d.name || src); this.skillErr = false; this.skillForm.src = ''; await this.loadSkill(); }
        else { this.skillMsg = '失败: ' + (d.error || ''); this.skillErr = true; }
      } catch { this.skillMsg = '失败'; this.skillErr = true; }
      this.skillInstalling = false;
    },
    async deleteSkill(name) {
      await this.post('/api/skill-delete', { name });
      setTimeout(() => this.loadSkill(), 200);
    },
    // fmtElapsed 镜像 TUI 的 formatElapsed:<60s → "1.2s";<1h → "1m05s";否则 "1h05m"。
    fmtElapsed(ms) {
      if (!ms || ms <= 0) return '0s';
      const sec = ms / 1000;
      if (sec < 60) return sec.toFixed(1) + 's';
      const totalS = Math.floor(sec);
      const m = Math.floor(totalS / 60);
      if (m < 60) return m + 'm' + String(totalS % 60).padStart(2, '0') + 's';
      const h = Math.floor(m / 60);
      return h + 'h' + String(m % 60).padStart(2, '0') + 'm';
    },

    // 用整份快照重置状态(新连接 / 重连)
    applySnapshot(s) {
      this.messages = s.messages || [];
      this.plan = s.plan || [];
      this.step = s.step || [];
      this.toolCalls = s.toolCalls || [];
      this.usage = s.usage || null;
      this.streaming = !!s.streaming;
      // 重连时无法还原精确起始时刻:streaming 则从现在起计,否则状态置 idle。
      if (this.streaming) {
        this.status = 'thinking';
        this.turnStart = Date.now();
        this.nowTick = Date.now();
      } else {
        this.status = 'idle';
        this.turnElapsed = 0;
      }
      this.models = s.models || this.models;
      this.workspace = s.workspace || '';
      this.reviewPending = s.reviewPending || null;
      this.askPending = (s.askQuestions && s.askQuestions.length) ? s.askQuestions : null;
      this.askSel = (this.askPending || []).map(q => (q.options || []).map(() => false));
      if (s.lang) this.lang = s.lang;
      if (s.vendor) this.vendor = s.vendor;
      if (s.routing) this.routing = s.routing;
      if (s.mode) this.mode = s.mode;
      if (s.sandbox) this.sandbox = s.sandbox;
      if (s.workingMode) this.workingMode = s.workingMode;
      this.codegraph = s.codegraph || '';
      this.balance = s.balance || '';
      this.showThinking = !!s.showThinking;
      this.sessions = s.sessions || [];
      this.queued = s.queued || [];
      // 推断流式气泡:最后一条是 assistant / thinking 且还在 streaming 则继续往里追加
      const last = this.messages.length - 1;
      const lastRole = last >= 0 ? this.messages[last].role : '';
      this.openIdx = (this.streaming && lastRole === 'assistant') ? last : -1;
      this.openThinkingIdx = (this.streaming && lastRole === 'thinking') ? last : -1;
      this.scrollDown();
    },

    // 应用单条增量事件(与后端 hub.apply 同构)
    applyEvent(ev) {
      switch (ev.kind) {
        case 'user_message':
          this.messages.push({ role: 'user', content: ev.text || '' });
          this.plan = [];
          this.step = [];
          this.toolCalls = [];
          // usage 不清空:保留上一轮的上下文用量,直到本轮 usage 事件回来整份覆盖,
          // 避免流式期间「上下文」面板先跳成 "—" 再出现新值(与后端 hub.apply 同构)。
          this.reviewPending = null;
          this.streaming = true;
          this.openIdx = -1;
          this.openThinkingIdx = -1;
          // 状态行:开新一轮,起计时
          this.status = 'thinking';
          this.turnStart = Date.now();
          this.turnElapsed = 0;
          this.nowTick = Date.now();
          break;
        case 'token':
          this.openThinkingIdx = -1; // 正式回复开始,思考段结束
          if (this.openIdx < 0) {
            this.messages.push({ role: 'assistant', content: '' });
            this.openIdx = this.messages.length - 1;
          }
          this.messages[this.openIdx].content += ev.text || '';
          this.status = 'streaming';
          break;
        case 'reasoning_token':
          this.status = 'thinking';
          // 暗显思考流:内联成 thinking 消息,天然排在随后助手回复之前(对齐 TUI)
          if (this.showThinking) {
            if (this.openThinkingIdx < 0) {
              this.messages.push({ role: 'thinking', content: '' });
              this.openThinkingIdx = this.messages.length - 1;
              this.openIdx = -1;
            }
            this.messages[this.openThinkingIdx].content += ev.text || '';
          }
          break;
        case 'tool_call':
          this.toolCalls.push({ id: ev.id, name: ev.name, args: ev.args || '', status: 'running', output: '' });
          // 内联进对话流;工具后另起 assistant 气泡。
          this.messages.push({ role: 'tool', id: ev.id, name: ev.name, args: ev.args || '', status: 'running', output: '' });
          this.openIdx = -1;
          this.openThinkingIdx = -1;
          this.status = 'tool';
          break;
        case 'tool_result': {
          const t = this.toolCalls.find((x) => x.id === ev.id) ||
            [...this.toolCalls].reverse().find((x) => x.name === ev.name && x.status === 'running');
          if (t) {
            t.status = ev.success ? 'done' : 'failed';
            t.output = ev.output || '';
          }
          // 同步对话流里的工具条(同样的配对:id 优先,否则同名 running 兜底)。
          const mt = [...this.messages].reverse().find(
            (x) => x.role === 'tool' && (x.id === ev.id || (x.name === ev.name && x.status === 'running'))
          );
          if (mt) {
            mt.status = ev.success ? 'done' : 'failed';
            mt.output = ev.output || '';
          }
          break;
        }
        case 'model_switch':
          if (ev.role) this.models.activeRole = ev.role;
          break;
        case 'models':
          if (ev.flash) this.models.flash = ev.flash;
          if (ev.pro) this.models.pro = ev.pro;
          if (ev.role) this.models.activeRole = ev.role;
          break;
        case 'plan':
          // createplan → 步骤;todo / 其它 → 计划(对齐 TUI 与后端 hub)
          if (ev.planKind === 'createplan') this.step = ev.plan || [];
          else this.plan = ev.plan || [];
          break;
        case 'plan_status': {
          // 节点 id 可能在 计划 或 步骤 任一份里
          const p = this.plan.find((x) => x.id === ev.id) || this.step.find((x) => x.id === ev.id);
          if (p) {
            if (ev.status) p.status = ev.status;
            if (ev.summary) p.summary = ev.summary;
          }
          break;
        }
        case 'usage':
          this.usage = ev.usage || null;
          break;
        case 'done':
        case 'error':
          this.streaming = false;
          this.openIdx = -1;
          this.openThinkingIdx = -1;
          // 冻结本轮总耗时,状态切到 完成 / 出错
          if (this.turnStart) this.turnElapsed = Date.now() - this.turnStart;
          this.status = ev.kind === 'error' ? 'error' : 'idle';
          break;
        case 'review_request':
          this.reviewPending = { name: ev.name, args: ev.args || '' };
          break;
        case 'review_resolved':
          this.reviewPending = null;
          break;
        case 'ask_request':
          this.askPending = ev.questions || null;
          this.askSel = (ev.questions || []).map(q => (q.options || []).map(() => false));
          break;
        case 'ask_resolved':
          this.askPending = null;
          this.askSel = [];
          break;
        case 'queued':
          // 流式/压缩中排队待发送列表整份覆盖(空 = 队列已清空),展示在输入框上方(issue #161)
          this.queued = ev.queued || [];
          break;
        case 'interrupted':
          this.streaming = false;
          this.openIdx = -1;
          this.openThinkingIdx = -1;
          this.reviewPending = null;
          this.askPending = null;
          this.askSel = [];
          this.queued = [];
          if (this.turnStart) this.turnElapsed = Date.now() - this.turnStart;
          this.status = 'idle';
          break;
        case 'notice':
          this.showToast(ev.text || '');
          break;
        case 'lang':
          if (ev.text) this.lang = ev.text;
          break;
        case 'vendor':
          if (ev.text) this.vendor = ev.text;
          break;
        case 'routing':
          if (ev.text) this.routing = ev.text;
          break;
        case 'mode':
          if (ev.text) this.mode = ev.text;
          break;
        case 'sandbox':
          if (ev.text) this.sandbox = ev.text;
          break;
        case 'working_mode':
          if (ev.text) this.workingMode = ev.text;
          break;
        case 'codegraph':
          if (ev.text) this.codegraph = ev.text;
          break;
        case 'show_thinking':
          // 只影响之后的思考显示;已内联的思考消息保留(对齐 TUI 的切换语义)
          this.showThinking = ev.text === 'on';
          break;
        case 'balance':
          // 余额可能从有值变 "-"(切到不支持的供应商),直接覆盖,不用非空守卫。
          this.balance = ev.text || '';
          break;
        case 'sessions':
          this.sessions = ev.sessions || [];
          break;
        case 'session_loaded':
          // 切换 / 新建会话:重置聊天与本轮派生状态。
          this.messages = ev.messages || [];
          this.plan = [];
          this.step = [];
          this.toolCalls = [];
          this.usage = null;
          this.reviewPending = null;
          this.streaming = false;
          this.openIdx = -1;
          this.queued = [];
          // 复位状态行,避免残留上一会话的"完成 · 用时"
          this.status = 'idle';
          this.turnElapsed = 0;
          break;
      }
      this.scrollDown();
    },

    // === 控制类:点按钮 POST 回 TUI,状态由后续 SSE 增量回灌(不本地乐观更新,保证与 TUI 一致)===
    async post(url, body) {
      await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: body ? JSON.stringify(body) : '{}',
      }).catch(() => {});
    },
    newSession() { this.post('/api/new'); },
    switchSession(id) { if (!this.streaming) this.post('/api/switch', { id }); },
    toggleMenu(id) { this.menuOpen = this.menuOpen === id ? null : id; },
    renameSession(s) {
      this.menuOpen = null;
      this.askPrompt({
        title: this.t('session.rename.prompt'),
        value: s.title || '',
        onOk: (title) => { if (title) this.post('/api/session-rename', { id: s.id, title }); },
      });
    },
    deleteSession(s) {
      this.menuOpen = null;
      if (s.id === 'default') { this.showToast(this.t('session.delete.default_hint')); return; } // 默认会话不可删,即时反馈(issue #141)
      // 用 app 内自定义确认弹窗,不用浏览器原生 confirm。
      this.askConfirm({
        text: this.t('session.delete.confirm'),
        danger: true,
        onYes: () => this.post('/api/session-delete', { id: s.id }),
      });
    },
    setLang(lang) { this.post('/api/lang', { lang }); },
    setModel(role) { this.post('/api/model', { role }); },
    setMode(m) { this.post('/api/mode', { mode: m }); },
    setSandbox(m) { this.post('/api/sandbox', { mode: m }); },
    setWorkingMode(m) { this.post('/api/workingmode', { mode: m }); },

    connect() {
      const es = new EventSource('/api/events');
      es.addEventListener('snapshot', (e) => {
        this.connected = true;
        this.applySnapshot(JSON.parse(e.data));
      });
      es.addEventListener('delta', (e) => {
        this.applyEvent(JSON.parse(e.data));
      });
      es.onerror = () => { this.connected = false; };
    },
  },
  mounted() {
    this.connect();
    // 点菜单以外的任何地方关闭"…"菜单(菜单按钮 / 菜单本身用 @click.stop 不会冒泡到这里)。
    document.addEventListener('click', () => { this.menuOpen = null; });
    // 状态行实时耗时:streaming 时每 250ms 刷新一次 nowTick 驱动重算。
    setInterval(() => { if (this.streaming) this.nowTick = Date.now(); }, 250);
  },
}).mount('#app');
