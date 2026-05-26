const { createApp, nextTick } = Vue;

createApp({
  data() {
    return {
      sessionId: "",
      username: "",
      pendingUsername: "",
      loginStep: "username",
      pwd: "/",
      input: "",
      history: [],
      historyIndex: -1,
      lines: [
        { type: "system", text: "PFS Web Terminal" },
      ],
      suggestions: [],
      loading: false,
      editor: {
        visible: false,
        filename: "",
        content: "",
        instance: null,
        language: "text",
      },
      userAdd: {
        active: false,
        username: "",
        step: "password",
        password: "",
      },
      diskDrawer: false,
      diskUsage: null,
      usedBlockSet: new Set(),
      diskConfig: {
        blockSize: 64,
        totalBlocks: 1024,
      },
      ws: null,
      wsConnected: false,
      lastEvent: "",
    };
  },
  computed: {
    loggedIn() {
      return this.sessionId !== "";
    },
    prompt() {
      if (!this.loggedIn) {
        return this.loginStep === "password" ? "password:" : "username:";
      }
      if (this.userAdd.active) {
        return this.userAdd.step === "confirm" ? "confirm:" : "password:";
      }
      return `${this.username}@FileSystem ${this.pwd} >`;
    },
    displayInput() {
      if ((!this.loggedIn && this.loginStep === "password") || this.userAdd.active) {
        return "•".repeat(this.input.length);
      }
      return this.input;
    },
    diskConfigCapacity() {
      return Number(this.diskConfig.blockSize) * Number(this.diskConfig.totalBlocks);
    },
    diskUsedBytes() {
      if (!this.diskUsage) return 0;
      return this.diskUsage.files.reduce((sum, file) => sum + file.size, 0);
    },
    diskConfigValid() {
      return (
        Number.isInteger(Number(this.diskConfig.blockSize)) &&
        Number.isInteger(Number(this.diskConfig.totalBlocks)) &&
        Number(this.diskConfig.blockSize) > 0 &&
        Number(this.diskConfig.totalBlocks) > 0 &&
        this.diskConfigCapacity >= this.diskUsedBytes
      );
    },
    diskConfigHint() {
      if (!this.diskUsage) return "";
      if (this.diskConfigValid) {
        return `新容量 ${this.diskConfigCapacity} B，当前文件数据 ${this.diskUsedBytes} B`;
      }
      return `新容量不足：至少需要 ${this.diskUsedBytes} B`;
    },
  },
  methods: {
    keepFocus() {
      this.focusInput();
    },
    async login(username, password) {
      this.loading = true;
      try {
        const data = await this.postJSON("/api/login", { username, password });
        this.sessionId = data.session_id;
        this.username = data.username;
        this.pwd = data.pwd;
        this.lines.push({ type: "output", text: data.output.trimEnd() });
        this.lines.push({ type: "system", text: "输入 help 查看命令，按 Tab 可补全命令或路径。" });
        this.connectEvents();
        await this.focusInput();
      } catch (error) {
        this.lines.push({ type: "error", text: error.message });
        this.pendingUsername = "";
        this.loginStep = "username";
      } finally {
        this.loading = false;
        await this.focusInput();
      }
    },
    async runCommand() {
      const rawLine = this.input;
      const line = rawLine.trim();
      if (!line && this.loginStep !== "password" && !this.userAdd.active) return;

      if (!this.loggedIn) {
        await this.runLoginStep(rawLine);
        return;
      }
      if (this.userAdd.active) {
        await this.runUserAddStep(rawLine);
        return;
      }

      this.lines.push({ type: "command", prompt: this.prompt, text: line });
      this.history.push(line);
      this.historyIndex = this.history.length;
      this.input = "";
      this.suggestions = [];

      if (line === "clear" || line === "cls") {
        this.lines = [];
      }

      this.loading = true;
      try {
        const data = await this.postJSON("/api/exec", {
          session_id: this.sessionId,
          line,
        });
        if (data.pwd) this.pwd = data.pwd;
        if (data.output) {
          this.lines.push({ type: data.exit ? "system" : "output", text: data.output.trimEnd() });
        }
        if (data.edit) {
          this.editor.filename = data.edit.filename;
          this.editor.content = data.edit.content || "";
          this.editor.language = this.editorLanguage(data.edit.filename);
          this.editor.visible = true;
        } else {
          await this.focusInput();
        }
        if (data.useradd) {
          this.userAdd.active = true;
          this.userAdd.username = data.useradd.username;
          this.userAdd.step = "password";
          this.userAdd.password = "";
        }
        if (this.diskDrawer) await this.loadDiskUsage();
        if (data.exit) {
          this.sessionId = "";
          this.username = "";
          this.pendingUsername = "";
          this.loginStep = "username";
          this.closeEvents();
        }
      } catch (error) {
        this.lines.push({ type: "error", text: error.message });
      } finally {
        this.loading = false;
        await this.focusInput();
        this.scrollBottom();
      }
    },
    async runLoginStep(rawLine) {
      const value = rawLine.trim();
      const displayText = this.loginStep === "password" ? "••••••" : value;
      this.lines.push({ type: "command", prompt: this.prompt, text: displayText });
      this.input = "";
      this.suggestions = [];

      if (this.loginStep === "username") {
        if (!value) {
          await this.focusInput();
          return;
        }
        this.pendingUsername = value;
        this.loginStep = "password";
        await this.focusInput();
        this.scrollBottom();
        return;
      }

      await this.login(this.pendingUsername, rawLine);
      await this.focusInput();
      this.scrollBottom();
    },
    async runUserAddStep(rawLine) {
      this.lines.push({ type: "command", prompt: this.prompt, text: "••••••" });
      this.input = "";
      this.suggestions = [];

      if (this.userAdd.step === "password") {
        if (!rawLine) {
          this.lines.push({ type: "error", text: "password cannot be empty" });
          await this.focusInput();
          return;
        }
        this.userAdd.password = rawLine;
        this.userAdd.step = "confirm";
        await this.focusInput();
        this.scrollBottom();
        return;
      }

      if (rawLine !== this.userAdd.password) {
        this.lines.push({ type: "error", text: "passwords do not match" });
        this.userAdd.step = "password";
        this.userAdd.password = "";
        await this.focusInput();
        this.scrollBottom();
        return;
      }

      this.loading = true;
      try {
        const data = await this.postJSON("/api/user/create", {
          session_id: this.sessionId,
          username: this.userAdd.username,
          password: rawLine,
        });
        if (data.pwd) this.pwd = data.pwd;
        if (data.output) this.lines.push({ type: "output", text: data.output.trimEnd() });
      } catch (error) {
        this.lines.push({ type: "error", text: error.message });
      } finally {
        this.loading = false;
        this.userAdd.active = false;
        this.userAdd.username = "";
        this.userAdd.step = "password";
        this.userAdd.password = "";
        if (this.diskDrawer) await this.loadDiskUsage();
        await this.focusInput();
        this.scrollBottom();
      }
    },
    async saveEditor() {
      this.loading = true;
      try {
        this.syncEditorContent();
        const data = await this.postJSON("/api/file/save", {
          session_id: this.sessionId,
          filename: this.editor.filename,
          content: this.editor.content,
        });
        if (data.pwd) this.pwd = data.pwd;
        if (data.output) this.lines.push({ type: "output", text: data.output.trimEnd() });
        this.editor.visible = false;
        this.destroyCodeEditor();
        if (this.diskDrawer) await this.loadDiskUsage();
        await this.focusInput();
      } catch (error) {
        this.lines.push({ type: "error", text: error.message });
      } finally {
        this.loading = false;
        await this.focusInput();
        this.scrollBottom();
      }
    },
    async openDiskDrawer() {
      if (!this.loggedIn) return;
      this.diskDrawer = !this.diskDrawer;
      if (this.diskDrawer) await this.loadDiskUsage();
      await this.focusInput();
    },
    async loadDiskUsage() {
      if (!this.loggedIn) return;
      try {
        this.diskUsage = await this.postJSON("/api/disk/usage", {
          session_id: this.sessionId,
        });
        this.usedBlockSet = new Set(this.diskUsage.used_block_ids || []);
        this.diskConfig.blockSize = this.diskUsage.block_size;
        this.diskConfig.totalBlocks = this.diskUsage.total_blocks;
      } catch (error) {
        this.lines.push({ type: "error", text: error.message });
      }
    },
    connectEvents() {
      this.closeEvents();
      const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      this.ws = new WebSocket(`${protocol}//${window.location.host}/ws/events`);
      this.ws.onopen = () => {
        this.wsConnected = true;
        this.lastEvent = "connected";
      };
      this.ws.onmessage = async (event) => {
        let payload = null;
        try {
          payload = JSON.parse(event.data);
        } catch {
          return;
        }
        this.lastEvent = payload.message || payload.type;
        if ((payload.type === "disk_changed" || payload.type === "user_changed") && this.diskDrawer) {
          await this.loadDiskUsage();
        }
      };
      this.ws.onclose = () => {
        this.wsConnected = false;
        if (this.loggedIn) {
          window.setTimeout(() => {
            if (this.loggedIn && (!this.ws || this.ws.readyState === WebSocket.CLOSED)) {
              this.connectEvents();
            }
          }, 1200);
        }
      };
      this.ws.onerror = () => {
        this.wsConnected = false;
      };
    },
    closeEvents() {
      if (this.ws) {
        this.ws.onclose = null;
        this.ws.close();
      }
      this.ws = null;
      this.wsConnected = false;
      this.lastEvent = "";
    },
    async applyDiskConfig() {
      if (!this.loggedIn) return;
      if (!this.diskConfigValid) {
        this.lines.push({ type: "error", text: this.diskConfigHint });
        this.scrollBottom();
        return;
      }
      this.loading = true;
      try {
        const data = await this.postJSON("/api/disk/config", {
          session_id: this.sessionId,
          block_size: Number(this.diskConfig.blockSize),
          total_blocks: Number(this.diskConfig.totalBlocks),
        });
        this.lines.push({ type: "output", text: data.message });
        await this.loadDiskUsage();
      } catch (error) {
        this.lines.push({ type: "error", text: error.message });
      } finally {
        this.loading = false;
        await this.focusInput();
        this.scrollBottom();
      }
    },
    isBlockUsed(blockIndex) {
      return this.usedBlockSet.has(blockIndex);
    },
    blockRange(blockIds) {
      if (!blockIds || blockIds.length === 0) return "-";
      return blockIds.join(", ");
    },
    editorMode(filename) {
      const ext = filename.split(".").pop().toLowerCase();
      const modes = {
        c: "c_cpp",
        h: "c_cpp",
        cpp: "c_cpp",
        cc: "c_cpp",
        cxx: "c_cpp",
        hpp: "c_cpp",
        java: "java",
        js: "javascript",
        json: "json",
        ts: "typescript",
        go: "golang",
        py: "python",
        sh: "sh",
        bash: "sh",
        md: "markdown",
        html: "html",
        htm: "html",
        css: "css",
        sql: "sql",
        xml: "xml",
      };
      return modes[ext] || "text";
    },
    editorLanguage(filename) {
      const ext = filename.split(".").pop().toLowerCase();
      const languages = {
        c: "c",
        h: "c",
        cpp: "cpp",
        cc: "cpp",
        cxx: "cpp",
        hpp: "cpp",
        java: "java",
        js: "javascript",
        json: "json",
        ts: "typescript",
        go: "go",
        py: "python",
        sh: "shell",
        bash: "shell",
        md: "markdown",
        html: "html",
        htm: "html",
        css: "css",
        sql: "sql",
        xml: "xml",
      };
      return languages[ext] || "text";
    },
    editorKeywords(language) {
      const shared = ["true", "false", "null", "return", "if", "else", "for", "while", "break", "continue"];
      const keywords = {
        c: ["#include", "#define", "int", "char", "float", "double", "void", "long", "short", "struct", "typedef", "sizeof", "printf", "scanf", "main"],
        cpp: ["#include", "using", "namespace", "std", "class", "public", "private", "protected", "template", "typename", "auto", "cout", "cin", "vector", "string"],
        java: ["class", "public", "private", "protected", "static", "final", "void", "int", "String", "new", "extends", "implements", "System", "out", "println"],
        javascript: ["const", "let", "var", "function", "async", "await", "return", "import", "export", "from", "class", "new", "this", "console", "log", "document", "window"],
        typescript: ["const", "let", "var", "function", "async", "await", "interface", "type", "string", "number", "boolean", "unknown", "Promise", "return"],
        go: ["package", "import", "func", "var", "const", "type", "struct", "interface", "defer", "go", "chan", "select", "range", "map", "string", "int", "error", "nil"],
        python: ["def", "class", "import", "from", "as", "self", "None", "True", "False", "lambda", "with", "try", "except", "finally", "print", "range"],
        shell: ["echo", "cd", "ls", "mkdir", "rm", "cp", "mv", "cat", "grep", "find", "if", "then", "else", "fi", "for", "do", "done", "export"],
        markdown: ["# ", "## ", "### ", "- ", "```", "[text](url)", "**bold**", "`code`"],
        html: ["html", "head", "body", "div", "span", "script", "style", "link", "meta", "class", "id"],
        css: ["display", "grid", "flex", "position", "color", "background", "border", "padding", "margin", "font-size", "line-height"],
        sql: ["SELECT", "FROM", "WHERE", "INSERT", "UPDATE", "DELETE", "CREATE", "TABLE", "JOIN", "GROUP BY", "ORDER BY", "LIMIT"],
        xml: ["xml", "version", "encoding"],
      };
      return [...shared, ...(keywords[language] || [])];
    },
    async setupCodeEditor() {
      await nextTick();
      if (!window.ace || !this.$refs.editorHost) return;
      this.destroyCodeEditor();

      window.ace.config.set("basePath", "https://unpkg.com/ace-builds@1.32.6/src-min-noconflict");
      const editor = window.ace.edit(this.$refs.editorHost);
      editor.setTheme("ace/theme/github");
      editor.session.setMode(`ace/mode/${this.editorMode(this.editor.filename)}`);
      editor.session.setValue(this.editor.content);
      editor.session.setUseWrapMode(false);
      editor.setOptions({
        fontFamily: 'Consolas, "Cascadia Mono", "Courier New", monospace',
        fontSize: "14px",
        tabSize: 4,
        useSoftTabs: true,
        showPrintMargin: false,
        enableBasicAutocompletion: true,
        enableLiveAutocompletion: true,
        enableSnippets: true,
      });
      editor.completers = [this.aceCompleter(), ...(window.ace.require("ace/ext/language_tools").textCompleter ? [window.ace.require("ace/ext/language_tools").textCompleter] : [])];
      editor.on("change", () => {
        this.editor.content = editor.getValue();
      });
      this.editor.instance = editor;

      window.setTimeout(() => {
        if (!this.editor.instance) return;
        this.editor.instance.resize(true);
        this.editor.instance.focus();
      }, 80);
    },
    aceCompleter() {
      return {
        getCompletions: (editor, session, position, prefix, callback) => {
          const words = new Set(this.editorKeywords(this.editor.language));
          const content = session.getValue();
          for (const match of content.matchAll(/[A-Za-z_#][A-Za-z0-9_#.-]{1,}/g)) {
            words.add(match[0]);
          }
          const completions = [...words]
            .filter((word) => !prefix || word.toLowerCase().startsWith(prefix.toLowerCase()))
            .sort((left, right) => left.localeCompare(right))
            .map((word) => ({
              caption: word,
              value: word,
              meta: this.editor.language,
              score: 1000,
            }));
          callback(null, completions);
        },
      };
    },
    syncEditorContent() {
      if (this.editor.instance) {
        this.editor.content = this.editor.instance.getValue();
      }
    },
    destroyCodeEditor() {
      if (!this.editor.instance) return;
      this.editor.instance.destroy();
      if (this.$refs.editorHost) {
        this.$refs.editorHost.innerHTML = "";
      }
      this.editor.instance = null;
    },
    async completeCommand(event) {
      event.preventDefault();
      if (!this.loggedIn || this.userAdd.active) return;

      const inputEl = this.$refs.commandInput;
      const cursor = inputEl.selectionStart ?? this.input.length;
      try {
        const data = await this.postJSON("/api/complete", {
          session_id: this.sessionId,
          line: this.input,
          cursor,
        });
        this.suggestions = data.suggestions || [];

        if (data.replacement !== undefined) {
          const before = this.input.slice(0, cursor);
          const after = this.input.slice(cursor);
          const tokenStart = Math.max(before.lastIndexOf(" "), before.lastIndexOf("\t")) + 1;
          this.input = before.slice(0, tokenStart) + data.replacement + after;
          await nextTick();
          const nextCursor = tokenStart + data.replacement.length;
          inputEl.setSelectionRange(nextCursor, nextCursor);
        }
        await this.focusInput();
      } catch (error) {
        this.lines.push({ type: "error", text: error.message });
      }
    },
    handleInput(event) {
      this.input = event.target.value;
    },
    handlePasswordKeydown(event) {
      const masking = (!this.loggedIn && this.loginStep === "password") || this.userAdd.active;
      if (!masking) return false;
      if (event.ctrlKey || event.metaKey || event.altKey) return false;

      if (event.key.length === 1) {
        event.preventDefault();
        this.input += event.key;
        return true;
      }
      if (event.key === "Backspace") {
        event.preventDefault();
        this.input = this.input.slice(0, -1);
        return true;
      }
      if (event.key === "Delete") {
        event.preventDefault();
        return true;
      }
      return false;
    },
    useHistory(direction) {
      if (!this.loggedIn) return;
      if (this.history.length === 0) return;
      this.historyIndex += direction;
      if (this.historyIndex < 0) this.historyIndex = 0;
      if (this.historyIndex > this.history.length) this.historyIndex = this.history.length;
      this.input = this.history[this.historyIndex] || "";
      this.suggestions = [];
    },
    async postJSON(url, body) {
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "request failed");
      return data;
    },
    async focusInput() {
      await nextTick();
      if (!this.editor.visible && this.$refs.commandInput) {
        this.$refs.commandInput.focus({ preventScroll: true });
      }
    },
    scrollBottom() {
      nextTick(() => {
        const terminal = this.$refs.terminal;
        if (terminal) terminal.scrollTop = terminal.scrollHeight;
      });
    },
  },
  mounted() {
    this.focusInput();
  },
  template: `
    <main class="shell-page" :class="{ 'with-disk-panel': diskDrawer }">
      <section class="topbar">
        <div>
          <h1>PFS Web Terminal</h1>
          <p>{{ loggedIn ? prompt : 'login required' }}</p>
        </div>
        <div class="topbar-actions">
          <el-button
            :type="diskDrawer ? 'primary' : 'default'"
            :disabled="!loggedIn"
            plain
            @click="openDiskDrawer"
          >
            磁盘块
          </el-button>
          <el-tag effect="dark" :type="loggedIn && wsConnected ? 'success' : loggedIn ? 'warning' : 'info'">
            {{ loggedIn ? (wsConnected ? 'online' : 'reconnecting') : 'offline' }}
          </el-tag>
        </div>
      </section>

      <section class="terminal-wrap">
        <div ref="terminal" class="terminal" @mousedown.prevent="keepFocus" @click="focusInput">
          <div v-for="(line, index) in lines" :key="index" class="terminal-line" :class="line.type">
            <template v-if="line.type === 'command'">
              <span class="prompt">{{ line.prompt }}</span>
              <span>{{ line.text }}</span>
            </template>
            <template v-else>
              <span>{{ line.text }}</span>
            </template>
          </div>
          <div class="input-row">
            <span class="prompt">{{ prompt }}</span>
            <input
              ref="commandInput"
              :value="displayInput"
              type="text"
              :disabled="loading"
              autocomplete="off"
              autocapitalize="off"
              autocorrect="off"
              spellcheck="false"
              @blur="keepFocus"
              @input="handleInput"
              @keydown.enter.prevent="runCommand"
              @keydown.tab.prevent="completeCommand"
              @keydown="handlePasswordKeydown"
              @keydown.up.prevent="useHistory(-1)"
              @keydown.down.prevent="useHistory(1)"
            />
          </div>
        </div>
        <div v-if="suggestions.length > 1" class="suggestions">
          <el-tag v-for="item in suggestions" :key="item" size="small">{{ item }}</el-tag>
        </div>
      </section>

      <el-dialog
        v-model="editor.visible"
        :modal="false"
        :close-on-click-modal="false"
        :title="'编辑 ' + editor.filename"
        width="min(760px, 92vw)"
        @opened="setupCodeEditor"
        @closed="destroyCodeEditor(); focusInput()"
        @mousedown.stop
      >
        <div ref="editorHost" class="code-editor" @mousedown.stop @click.stop @keydown.stop></div>
        <template #footer>
          <el-button @click="editor.visible = false; destroyCodeEditor(); focusInput()">取消</el-button>
          <el-button type="primary" :loading="loading" @click="saveEditor">保存</el-button>
        </template>
      </el-dialog>

      <aside v-show="diskDrawer" class="disk-side-panel">
        <header class="disk-side-header">
          <div>
            <h2>磁盘块空间</h2>
            <p v-if="diskUsage">实时统计<span v-if="lastEvent"> · {{ lastEvent }}</span></p>
          </div>
          <div class="disk-side-actions">
            <el-button size="small" text @click="diskDrawer = false; focusInput()">关闭</el-button>
          </div>
        </header>
        <div v-if="diskUsage" class="disk-panel">
          <section class="disk-config">
            <label>
              <span>块大小 B</span>
              <el-input-number
                v-model="diskConfig.blockSize"
                :min="1"
                :step="16"
                size="small"
                controls-position="right"
              />
            </label>
            <label>
              <span>总块数</span>
              <el-input-number
                v-model="diskConfig.totalBlocks"
                :min="1"
                :step="128"
                size="small"
                controls-position="right"
              />
            </label>
            <p :class="{ invalid: !diskConfigValid }">{{ diskConfigHint }}</p>
            <el-button
              type="primary"
              :disabled="!diskConfigValid"
              :loading="loading"
              @click="applyDiskConfig"
            >
              应用迁移
            </el-button>
          </section>
          <el-progress
            :percentage="Number(diskUsage.used_percent.toFixed(1))"
            :stroke-width="14"
            status="success"
          />
          <div class="disk-stats">
            <div>
              <span>块大小</span>
              <strong>{{ diskUsage.block_size }} B</strong>
            </div>
            <div>
              <span>总块数</span>
              <strong>{{ diskUsage.total_blocks }}</strong>
            </div>
            <div>
              <span>已用块</span>
              <strong>{{ diskUsage.used_blocks }}</strong>
            </div>
            <div>
              <span>空闲块</span>
              <strong>{{ diskUsage.free_blocks }}</strong>
            </div>
          </div>
          <div class="block-strip">
            <span
              v-for="index in diskUsage.total_blocks"
              :key="index"
              :class="{ used: isBlockUsed(index - 1) }"
              :title="'block ' + (index - 1)"
            ></span>
          </div>
          <el-table :data="diskUsage.files" size="small" height="320" empty-text="暂无文件占用磁盘块">
            <el-table-column prop="path" label="文件" min-width="150" show-overflow-tooltip />
            <el-table-column prop="size" label="大小 B" width="78" />
            <el-table-column prop="blocks" label="块" width="60" />
            <el-table-column label="块号" min-width="130" show-overflow-tooltip>
              <template #default="{ row }">{{ blockRange(row.block_ids) }}</template>
            </el-table-column>
          </el-table>
        </div>
      </aside>
    </main>
  `,
}).use(ElementPlus).mount("#app");
