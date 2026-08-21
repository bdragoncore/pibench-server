(() => {
  const log = document.getElementById('log');
  const form = document.getElementById('chat-form');
  const input = document.getElementById('chat-input');
  const sendBtn = document.getElementById('send-btn');
  const sessionSelect = document.getElementById('session-select');
  const sessionDropdownWrap = document.getElementById('session-dropdown-wrap');
  const sessionDropdownBtn = document.getElementById('session-dropdown-btn');
  const sessionDropdownTitle = document.getElementById('session-dropdown-title');
  const sessionDropdownMenu = document.getElementById('session-dropdown-menu');
  const sessionItemsList = document.getElementById('session-items-list');
  const newSessionBtn = document.getElementById('new-session');
  const deleteSessionBtn = document.getElementById('delete-session');
  const btnActions = document.getElementById('btn-actions');
  const actionsMenu = document.getElementById('actions-menu');
  const optClear = document.getElementById('opt-clear');
  const optExportJson = document.getElementById('opt-export-json');
  const optExportMd = document.getElementById('opt-export-md');
  const optSyncModels = document.getElementById('opt-sync-models');
  const modelBadge = document.getElementById('model-badge');
  const modelSelectBadge = document.getElementById('model-select-badge');
  const statusBar = document.getElementById('status-bar');
  const statusText = document.getElementById('status-text');

  let currentSessionId = null;
  let activeToolCards = {}; // tcKey -> element
  let isGenerating = false;
  let currentMessages = []; // stored raw messages for export

  // Escaping helper
  function escapeHtml(str) {
    if (!str) return '';
    return str
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }

  // Lightweight robust Markdown parser
  function parseMarkdown(md) {
    if (!md) return '';
    
    // 1. Code blocks ```lang ... ```
    const codeBlocks = [];
    let text = md.replace(/```([a-zA-Z0-9_-]*)\n([\s\S]*?)```/g, (match, lang, code) => {
      const idx = codeBlocks.length;
      codeBlocks.push({ lang: lang || 'code', code: code });
      return `___CODE_BLOCK_${idx}___`;
    });

    // Escape HTML outside of code blocks
    text = escapeHtml(text);

    // Restore and render code blocks
    text = text.replace(/___CODE_BLOCK_(\d+)___/g, (match, idx) => {
      const item = codeBlocks[parseInt(idx, 10)];
      const escapedCode = escapeHtml(item.code);
      return `<div class="code-block-wrap">
        <div class="code-block-header">
          <span>${escapeHtml(item.lang)}</span>
          <button class="copy-code-btn" data-code="${encodeURIComponent(item.code)}">Copy</button>
        </div>
        <pre class="code-block"><code>${escapedCode}</code></pre>
      </div>`;
    });

    // 2. Inline code `code`
    text = text.replace(/`([^`]+)`/g, (match, code) => {
      return `<code class="inline-code">${code}</code>`;
    });

    // 3. Headers
    text = text.replace(/^### (.*$)/gim, '### $1');
    text = text.replace(/^## (.*$)/gim, '## $1');
    text = text.replace(/^# (.*$)/gim, '# $1');

    // 4. Bold & Italic
    text = text.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    text = text.replace(/\*([^*]+)\*/g, '<em>$1</em>');

    // 5. Blockquotes
    text = text.replace(/^\&gt;\s?(.*$)/gim, '<blockquote>$1</blockquote>');

    // 6. Links [text](url)
    text = text.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');

    // 7. Line breaks
    text = text.replace(/\n/g, '<br>');

    // Clean up excessive <br> around code blocks
    text = text.replace(/<br>\s*<div class="code-block-wrap">/g, '<div class="code-block-wrap">');
    text = text.replace(/<\/div>\s*<br>/g, '</div>');

    return text;
  }

  // Copy code handler
  document.addEventListener('click', (e) => {
    if (e.target && e.target.classList.contains('copy-code-btn')) {
      const code = decodeURIComponent(e.target.getAttribute('data-code') || '');
      navigator.clipboard.writeText(code).then(() => {
        const orig = e.target.textContent;
        e.target.textContent = 'Copied!';
        setTimeout(() => { e.target.textContent = orig; }, 2000);
      });
    }
  });

  // Render Welcome Screen if no messages exist
  function renderWelcomeScreen() {
    log.innerHTML = '';
    const welcome = document.createElement('div');
    welcome.className = 'welcome-container';
    welcome.innerHTML = `
      <div class="welcome-card">
        <h2>⚡ OpenCode Agent</h2>
        <p>A minimal, high-performance agentic coding assistant. Ask me to inspect files, execute terminal commands, or solve coding tasks.</p>
        <div class="tools-pill-bar">
          <span class="tool-pill">bash</span>
          <span class="tool-pill">read_file</span>
          <span class="tool-pill">write_file</span>
          <span class="tool-pill">edit_file</span>
        </div>
      </div>
      <div class="prompt-suggestions">
        <div class="prompt-chip" data-prompt="Check git status and recent commits">
          <span class="chip-icon">🔍</span>
          <span>Check git status</span>
        </div>
        <div class="prompt-chip" data-prompt="List files and directory structure in the workspace">
          <span class="chip-icon">📁</span>
          <span>Explore workspace files</span>
        </div>
        <div class="prompt-chip" data-prompt="Run project tests or check system environment">
          <span class="chip-icon">🛠️</span>
          <span>Run system checks</span>
        </div>
        <div class="prompt-chip" data-prompt="Explain the core entry points of this codebase">
          <span class="chip-icon">📝</span>
          <span>Explain codebase architecture</span>
        </div>
      </div>
    `;
    log.appendChild(welcome);

    // Prompt chip listeners
    welcome.querySelectorAll('.prompt-chip').forEach(chip => {
      chip.addEventListener('click', () => {
        const p = chip.getAttribute('data-prompt');
        if (p) {
          input.value = p;
          autoResizeInput();
          sendMessage(p);
        }
      });
    });
  }

  // Append or update message element
  function addMsg(role, text) {
    // Clear welcome screen if present
    if (log.querySelector('.welcome-container')) {
      log.innerHTML = '';
    }

    const el = document.createElement('div');
    el.className = 'msg ' + role;
    
    const label = document.createElement('span');
    label.className = 'label';
    label.textContent = role === 'user' ? 'You' : role === 'assistant' ? 'OpenCode' : role;
    el.appendChild(label);

    const body = document.createElement('div');
    body.className = role === 'assistant' ? 'msg-body' : 'msg-text';
    
    if (role === 'assistant') {
      body.innerHTML = parseMarkdown(text);
    } else {
      body.textContent = text;
    }
    el.appendChild(body);
    log.appendChild(el);
    log.scrollTop = log.scrollHeight;

    currentMessages.push({ role, content: text });
    return body;
  }

  function addSuperuserCard(reason) {
    if (log.querySelector('.welcome-container')) {
      log.innerHTML = '';
    }

    const card = document.createElement('div');
    card.className = 'tool-card superuser-card';
    card.innerHTML = `
      <div class="superuser-header">
        <div class="superuser-title">
          <span>🔑</span>
          <strong>Superuser Privilege Request</strong>
        </div>
        <span class="tool-status-tag warn">PROMPT</span>
      </div>
      <div class="superuser-body">
        <p class="superuser-reason">${escapeHtml(reason || 'OpenCode requires elevated superuser / sudo privileges to execute system commands.')}</p>
        <div class="superuser-input-row">
          <input type="password" class="sudo-password-input form-input" placeholder="Enter sudo password for pibench..." autocomplete="off">
          <button class="btn-grant-sudo btn-primary">Grant Sudo Access</button>
        </div>
        <div class="sudo-status-msg hidden"></div>
      </div>
    `;

    const input = card.querySelector('.sudo-password-input');
    const btnGrant = card.querySelector('.btn-grant-sudo');
    const statusMsg = card.querySelector('.sudo-status-msg');
    const statusTag = card.querySelector('.tool-status-tag');

    async function submitSudoPassword() {
      const pass = input.value.trim();
      if (!pass) return;

      statusMsg.textContent = 'Verifying password...';
      statusMsg.className = 'sudo-status-msg info';
      btnGrant.disabled = true;

      try {
        const res = await fetch(getApiUrl('api/superuser'), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ password: pass })
        });

        if (res.ok) {
          statusMsg.textContent = '✓ Superuser privileges granted!';
          statusMsg.className = 'sudo-status-msg success';
          statusTag.className = 'tool-status-tag done';
          statusTag.textContent = 'GRANTED';
          input.disabled = true;
          btnGrant.disabled = true;
          
          setTimeout(() => {
            sendMessage("Superuser access granted. You can now execute your sudo commands.");
          }, 800);
        } else {
          const errText = await res.text();
          statusMsg.textContent = '✕ Access denied: ' + (errText || 'Incorrect password');
          statusMsg.className = 'sudo-status-msg error-msg';
          btnGrant.disabled = false;
          input.focus();
        }
      } catch (err) {
        statusMsg.textContent = '✕ Connection error: ' + err.message;
        statusMsg.className = 'sudo-status-msg error-msg';
        btnGrant.disabled = false;
      }
    }

    btnGrant.addEventListener('click', submitSudoPassword);
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        submitSudoPassword();
      }
    });

    log.appendChild(card);
    log.scrollTop = log.scrollHeight;
    setTimeout(() => input.focus(), 100);
    return card;
  }

  // Render or update Tool Card
  function addToolCard(toolName, args, result = null, isDone = false) {
    if (toolName === 'superuser_access' || (result && result.includes('[SUPERUSER_REQUEST_REQUIRED]'))) {
      const reason = (result || '').replace('[SUPERUSER_REQUEST_REQUIRED]', '').trim() || args;
      return addSuperuserCard(reason);
    }

    if (log.querySelector('.welcome-container')) {
      log.innerHTML = '';
    }

    const tcKey = toolName + '_' + (args || '').slice(0, 30);
    let card = activeToolCards[tcKey];

    if (!card) {
      card = document.createElement('div');
      card.className = 'tool-card';
      card.innerHTML = `
        <div class="tool-card-header">
          <div class="tool-card-title">
            <span>🧰</span>
            <span class="tool-name">${escapeHtml(toolName)}</span>
            <span class="tool-args">${escapeHtml(args || '')}</span>
          </div>
          <div class="tool-card-controls">
            <span class="tool-status-tag ${isDone ? 'done' : 'running'}">${isDone ? 'DONE' : 'RUNNING'}</span>
            <button class="tool-toggle-btn" title="Toggle output">▼</button>
          </div>
        </div>
        <div class="tool-card-body">
          <pre class="tool-output">${escapeHtml(result || 'Executing command...')}</pre>
        </div>
      `;

      const header = card.querySelector('.tool-card-header');
      header.addEventListener('click', () => {
        card.classList.toggle('collapsed');
      });

      log.appendChild(card);
      activeToolCards[tcKey] = card;
    } else {
      const statusTag = card.querySelector('.tool-status-tag');
      const outputPre = card.querySelector('.tool-output');

      if (isDone) {
        statusTag.className = 'tool-status-tag done';
        statusTag.textContent = 'DONE';
        if (result) outputPre.textContent = result;
      } else {
        if (result) outputPre.textContent = result;
      }
    }

    log.scrollTop = log.scrollHeight;
    return card;
  }

  function getApiUrl(path) {
    const base = window.location.pathname.endsWith('/') ? window.location.pathname : window.location.pathname + '/';
    return base + path.replace(/^\//, '');
  }

  // System notification card helper
  function addSystemMsg(htmlContent) {
    const card = document.createElement('div');
    card.className = 'msg system-msg';
    card.innerHTML = `<div class="msg-content system-notice">${htmlContent}</div>`;
    log.appendChild(card);
    log.scrollTop = log.scrollHeight;
  }

  let cachedModelList = [];

  // Swap active model helper
  async function swapModel(newModel) {
    if (!newModel) return;
    try {
      const res = await fetch(getApiUrl('api/config'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: newModel }),
      });
      if (res.ok) {
        const data = await res.json();
        const active = data.active_model || newModel;
        const full = active.includes('/') ? active : `openmind/${active}`;
        if (modelSelectBadge) modelSelectBadge.value = full;
        if (settingsModelSelect) settingsModelSelect.value = full;
        addSystemMsg(`⚡ Active AI model swapped to <strong>${escapeHtml(active)}</strong>`);
      } else {
        addSystemMsg(`❌ Failed to swap model: ${res.statusText}`);
      }
    } catch (err) {
      console.error('Failed to swap model:', err);
      addSystemMsg(`❌ Error swapping model: ${err.message}`);
    }
  }

  // Fetch backend info
  async function loadInfo() {
    try {
      const res = await fetch(getApiUrl('api/config'));
      if (res.ok) {
        loadedConfigData = await res.json();
        updateModelDropdown(loadedConfigData.raw_json || loadedConfigData.default_config || '', loadedConfigData.active_model);
      }
    } catch (e) {
      console.error('loadInfo error:', e);
    }
  }

  // Sessions management
  async function refreshSessions(selectId) {
    try {
      const res = await fetch(getApiUrl('api/sessions'));
      const sessions = await res.json();
      sessionSelect.innerHTML = '';
      if (sessionItemsList) sessionItemsList.innerHTML = '';

      const targetId = selectId || currentSessionId;
      let activeTitle = '-- select chat --';

      if (!sessions || sessions.length === 0) {
        if (sessionDropdownTitle) sessionDropdownTitle.textContent = '(no saved chats)';
        deleteSessionBtn.classList.add('hidden');
        return;
      }

      for (const s of sessions || []) {
        const titleText = s.title || s.id.slice(0, 18);
        const isActive = s.id === targetId;

        if (isActive) {
          activeTitle = titleText;
        }

        const opt = document.createElement('option');
        opt.value = s.id;
        opt.textContent = titleText;
        if (isActive) opt.selected = true;
        sessionSelect.appendChild(opt);

        if (sessionItemsList) {
          const itemBtn = document.createElement('button');
          itemBtn.type = 'button';
          itemBtn.className = `session-item ${isActive ? 'active' : ''}`;
          itemBtn.innerHTML = `<span>${escapeHtml(titleText)}</span>`;
          itemBtn.addEventListener('click', () => {
            if (sessionDropdownMenu) sessionDropdownMenu.classList.add('hidden');
            if (sessionDropdownWrap) sessionDropdownWrap.classList.remove('open');
            if (sessionDropdownTitle) sessionDropdownTitle.textContent = titleText;
            loadSession(s.id);
          });
          sessionItemsList.appendChild(itemBtn);
        }
      }

      if (sessionDropdownTitle) sessionDropdownTitle.textContent = activeTitle;

      if (targetId) {
        deleteSessionBtn.classList.remove('hidden');
      } else {
        deleteSessionBtn.classList.add('hidden');
      }
    } catch (err) {
      console.error('Failed to fetch sessions:', err);
    }
  }

  if (sessionDropdownBtn) {
    sessionDropdownBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      sessionDropdownMenu.classList.toggle('hidden');
      sessionDropdownWrap.classList.toggle('open');
      if (!sessionDropdownMenu.classList.contains('hidden') && sessionSearchInput) {
        sessionSearchInput.value = '';
        const items = sessionItemsList.querySelectorAll('.session-item');
        items.forEach(item => item.style.display = 'flex');
        setTimeout(() => sessionSearchInput.focus(), 50);
      }
    });
  }

  const sessionSearchInput = document.getElementById('session-search-input');
  if (sessionSearchInput) {
    sessionSearchInput.addEventListener('input', (e) => {
      const q = e.target.value.toLowerCase().trim();
      if (!sessionItemsList) return;
      const items = sessionItemsList.querySelectorAll('.session-item');
      items.forEach(item => {
        const text = item.textContent.toLowerCase();
        if (!q || text.includes(q)) {
          item.style.display = 'flex';
        } else {
          item.style.display = 'none';
        }
      });
    });
  }

  async function loadSession(id) {
    log.innerHTML = '';
    activeToolCards = {};
    currentMessages = [];
    currentSessionId = id;

    if (!id) {
      deleteSessionBtn.classList.add('hidden');
      renderWelcomeScreen();
      return;
    }

    deleteSessionBtn.classList.remove('hidden');

    try {
      const res = await fetch(getApiUrl(`api/sessions/${id}/messages`));
      if (!res.ok) return;
      const msgs = await res.json();

      if (!msgs || msgs.length === 0) {
        renderWelcomeScreen();
        return;
      }

      for (const m of msgs || []) {
        if (m.role === 'user') {
          addMsg('user', m.content);
        } else if (m.role === 'assistant' && m.content) {
          addMsg('assistant', m.content);
        } else if (m.role === 'tool') {
          addToolCard('tool', '', m.content, true);
        }
      }
    } catch (err) {
      console.error('Failed to load session messages:', err);
    }
  }

  async function deleteCurrentSession() {
    if (!currentSessionId) return;
    if (!confirm('Are you sure you want to delete this chat session?')) return;

    try {
      const res = await fetch(getApiUrl(`api/sessions/${currentSessionId}`), { method: 'DELETE' });
      if (res.ok) {
        currentSessionId = null;
        sessionSelect.value = '';
        if (sessionDropdownTitle) sessionDropdownTitle.textContent = '-- select chat --';
        deleteSessionBtn.classList.add('hidden');
        await refreshSessions();
        renderWelcomeScreen();
      }
    } catch (err) {
      alert('Failed to delete session: ' + err);
    }
  }

  // Auto resize input textarea
  function autoResizeInput() {
    input.style.height = 'auto';
    input.style.height = Math.min(input.scrollHeight, 160) + 'px';
  }

  input.addEventListener('input', autoResizeInput);

  // Keyboard navigation for sending
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      form.dispatchEvent(new Event('submit'));
    }
  });

  // Event Listeners
  sessionSelect.addEventListener('change', () => loadSession(sessionSelect.value));

  newSessionBtn.addEventListener('click', () => {
    currentSessionId = null;
    sessionSelect.value = '';
    if (sessionDropdownTitle) sessionDropdownTitle.textContent = '-- select chat --';
    if (sessionItemsList) {
      sessionItemsList.querySelectorAll('.session-item').forEach(el => el.classList.remove('active'));
    }
    deleteSessionBtn.classList.add('hidden');
    renderWelcomeScreen();
    input.focus();
  });

  deleteSessionBtn.addEventListener('click', deleteCurrentSession);

  // Actions menu toggle
  btnActions.addEventListener('click', (e) => {
    e.stopPropagation();
    actionsMenu.classList.toggle('hidden');
  });

  document.addEventListener('click', () => {
    actionsMenu.classList.add('hidden');
  });

  optClear.addEventListener('click', () => {
    log.innerHTML = '';
    activeToolCards = {};
    renderWelcomeScreen();
  });

  optExportJson.addEventListener('click', () => {
    const dataStr = "data:text/json;charset=utf-8," + encodeURIComponent(JSON.stringify(currentMessages, null, 2));
    const dlAnchor = document.createElement('a');
    dlAnchor.setAttribute("href", dataStr);
    dlAnchor.setAttribute("download", `opencode-session-${currentSessionId || 'export'}.json`);
    document.body.appendChild(dlAnchor);
    dlAnchor.click();
    dlAnchor.remove();
  });

  optExportMd.addEventListener('click', () => {
    let mdText = `# OpenCode Chat Export\n\n`;
    for (const m of currentMessages) {
      mdText += `### ${m.role.toUpperCase()}\n${m.content}\n\n---\n\n`;
    }
    const dataStr = "data:text/markdown;charset=utf-8," + encodeURIComponent(mdText);
    const dlAnchor = document.createElement('a');
    dlAnchor.setAttribute("href", dataStr);
    dlAnchor.setAttribute("download", `opencode-session-${currentSessionId || 'export'}.md`);
    document.body.appendChild(dlAnchor);
    dlAnchor.click();
    dlAnchor.remove();
  });

  optToggleTools.addEventListener('click', () => {
    const cards = log.querySelectorAll('.tool-card');
    const anyExpanded = Array.from(cards).some(c => !c.classList.contains('collapsed'));
    cards.forEach(c => {
      if (anyExpanded) c.classList.add('collapsed');
      else c.classList.remove('collapsed');
    });
  });

  // Main Submit handler
  async function sendMessage(text) {
    if (!text || isGenerating) return;

    if (text.startsWith('/sync')) {
      input.value = '';
      autoResizeInput();
      triggerSyncModels();
      return;
    }

    if (text.startsWith('/model')) {
      input.value = '';
      autoResizeInput();
      const arg = text.slice(6).trim();
      if (!arg) {
        if (cachedModelList.length > 0) {
          let html = `<strong>Available Models (click to swap):</strong><div class="model-pills-wrap" style="display:flex; flex-wrap:wrap; gap:0.4rem; margin-top:0.5rem;">`;
          cachedModelList.forEach(m => {
            const shortName = m.replace(/^openmind\//, '');
            html += `<button type="button" class="btn-sm model-pill-btn" data-model="${escapeHtml(m)}" style="background:var(--panel-2); border:1px solid var(--border-strong); color:var(--accent); border-radius:12px; padding:0.25rem 0.65rem; cursor:pointer; font-size:0.8rem; font-weight:600;">${escapeHtml(shortName)}</button>`;
          });
          html += `</div>`;
          addSystemMsg(html);
        } else {
          addSystemMsg(`Use the header model selector dropdown menu to swap models.`);
        }
      } else {
        const matched = cachedModelList.find(m => m.toLowerCase().includes(arg.toLowerCase()));
        if (matched) {
          await swapModel(matched);
        } else {
          await swapModel(arg);
        }
      }
      return;
    }

    input.value = '';
    autoResizeInput();
    sendBtn.disabled = true;
    isGenerating = true;

    statusBar.classList.remove('hidden');
    statusText.textContent = 'OpenCode is thinking...';

    addMsg('user', text);

    let assistantBody = null;
    let assistantText = '';

    try {
      const res = await fetch(getApiUrl('api/chat'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: currentSessionId, message: text }),
      });

      const sid = res.headers.get('X-Session-Id');
      if (sid && sid !== currentSessionId) {
        currentSessionId = sid;
        await refreshSessions(sid);
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });

        let idx;
        while ((idx = buf.indexOf('\n\n')) !== -1) {
          const frame = buf.slice(0, idx);
          buf = buf.slice(idx + 2);
          const line = frame.split('\n').find((l) => l.startsWith('data: '));
          if (!line) continue;
          const ev = JSON.parse(line.slice(6));

          if (ev.type === 'text') {
            if (!assistantBody) assistantBody = addMsg('assistant', '');
            assistantText += ev.text;
            assistantBody.innerHTML = parseMarkdown(assistantText);
            log.scrollTop = log.scrollHeight;
          } else if (ev.type === 'tool_call') {
            statusText.textContent = `Running tool ${ev.tool}...`;
            addToolCard(ev.tool, ev.args, null, false);
          } else if (ev.type === 'tool_result') {
            statusText.textContent = `Completed ${ev.tool}`;
            addToolCard(ev.tool, '', ev.output, true);
          } else if (ev.type === 'error') {
            addMsg('error', ev.message);
          } else if (ev.type === 'done') {
            statusText.textContent = 'Done';
          }
        }
      }
    } catch (err) {
      addMsg('error', String(err));
    } finally {
      sendBtn.disabled = false;
      isGenerating = false;
      statusBar.classList.add('hidden');
      input.focus();
    }
  }

  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const text = input.value.trim();
    if (text) sendMessage(text);
  });

  // Init
  loadInfo();
  refreshSessions();
  renderWelcomeScreen();

  // ---------- Provider Settings Modal ----------
  const btnSettings = document.getElementById('btn-settings');
  const optSettings = document.getElementById('opt-settings');
  const settingsModal = document.getElementById('settings-modal');
  const modalClose = document.getElementById('modal-close');
  const btnCancelSettings = document.getElementById('btn-cancel-settings');
  const btnSaveSettings = document.getElementById('btn-save-settings');
  const settingsModelSelect = document.getElementById('settings-model-select');
  const settingsBaseUrl = document.getElementById('settings-base-url');
  const jsonEditorTextarea = document.getElementById('json-editor-textarea');
  const jsonEditorError = document.getElementById('json-editor-error');
  const btnSyncModels = document.getElementById('btn-sync-models');
  const btnLoadOpenmindDefault = document.getElementById('btn-load-openmind-default');
  const btnFormatJson = document.getElementById('btn-format-json');
  const btnValidateJson = document.getElementById('btn-validate-json');
  const settingsStatus = document.getElementById('settings-status');

  let loadedConfigData = null;

  async function triggerSyncModels() {
    try {
      if (settingsStatus) settingsStatus.textContent = 'Syncing models from gateway...';
      const res = await fetch(getApiUrl('api/config/sync-models'), { method: 'POST' });
      if (res.ok) {
        const data = await res.json();
        if (jsonEditorTextarea) jsonEditorTextarea.value = data.raw_json || '';
        updateModelDropdown(data.raw_json || '', data.active_model);
        if (settingsStatus) {
          settingsStatus.textContent = `✓ Synced ${data.models_scraped || 0} live models!`;
          setTimeout(() => { settingsStatus.textContent = ''; }, 3000);
        }
        addSystemMsg(`⚡ Successfully scraped and updated <strong>${data.models_scraped || 0} live models</strong> from OpenMind gateway into opencode.json!`);
      } else {
        const errText = await res.text();
        if (settingsStatus) settingsStatus.textContent = '';
        addSystemMsg(`❌ Failed to sync models: ${errText}`);
      }
    } catch (err) {
      console.error('Sync models error:', err);
      if (settingsStatus) settingsStatus.textContent = '';
      addSystemMsg(`❌ Sync models error: ${err.message}`);
    }
  }

  function parseModelsFromConfig(cfgJsonStr) {
    const models = [];
    try {
      const cfg = JSON.parse(cfgJsonStr);
      if (cfg && cfg.provider) {
        for (const [pKey, pVal] of Object.entries(cfg.provider)) {
          if (pVal && pVal.models) {
            for (const [mKey, mVal] of Object.entries(pVal.models)) {
              models.push(`${pKey}/${mKey}`);
            }
          }
        }
      }
    } catch (e) {}
    return models;
  }

  function updateModelDropdown(cfgJsonStr, currentModel) {
    const models = parseModelsFromConfig(cfgJsonStr);
    cachedModelList = models;

    if (currentModel && !models.includes(currentModel) && !models.includes(`openmind/${currentModel}`)) {
      models.unshift(currentModel.includes('/') ? currentModel : `openmind/${currentModel}`);
    }

    const dropdowns = [settingsModelSelect, modelSelectBadge].filter(Boolean);

    dropdowns.forEach(select => {
      select.innerHTML = '';
      if (models.length === 0) {
        const opt = document.createElement('option');
        opt.value = currentModel || 'openmind/zen-hy3-free';
        opt.textContent = select === modelSelectBadge ? opt.value.replace(/^openmind\//, '') : opt.value;
        select.appendChild(opt);
      } else {
        models.forEach(m => {
          const opt = document.createElement('option');
          opt.value = m;
          opt.textContent = select === modelSelectBadge ? m.replace(/^openmind\//, '') : m;
          select.appendChild(opt);
        });
      }

      if (currentModel) {
        const fullModelName = currentModel.includes('/') ? currentModel : `openmind/${currentModel}`;
        if (Array.from(select.options).some(o => o.value === fullModelName)) {
          select.value = fullModelName;
        } else if (Array.from(select.options).some(o => o.value === currentModel)) {
          select.value = currentModel;
        }
      }
    });
  }

  if (modelSelectBadge) {
    modelSelectBadge.addEventListener('change', () => {
      swapModel(modelSelectBadge.value);
    });
  }

  async function openSettingsModal() {
    jsonEditorError.classList.add('hidden');
    jsonEditorError.textContent = '';
    settingsStatus.textContent = '';

    try {
      const res = await fetch(getApiUrl('api/config'));
      if (res.ok) {
        loadedConfigData = await res.json();
        jsonEditorTextarea.value = loadedConfigData.raw_json || loadedConfigData.default_config || '';
        settingsBaseUrl.value = loadedConfigData.active_base_url || 'http://pibox.local:5000/v1';
        updateModelDropdown(jsonEditorTextarea.value, loadedConfigData.active_model);
      }
    } catch (err) {
      console.error('Failed to load settings:', err);
    }
    settingsModal.classList.remove('hidden');
  }

  function closeSettingsModal() {
    settingsModal.classList.add('hidden');
  }

  jsonEditorTextarea.addEventListener('input', () => {
    jsonEditorError.classList.add('hidden');
    updateModelDropdown(jsonEditorTextarea.value, settingsModelSelect.value);
  });

  jsonEditorTextarea.addEventListener('keydown', (e) => {
    if (e.key === 'Tab') {
      e.preventDefault();
      const start = jsonEditorTextarea.selectionStart;
      const end = jsonEditorTextarea.selectionEnd;
      jsonEditorTextarea.value = jsonEditorTextarea.value.substring(0, start) + '  ' + jsonEditorTextarea.value.substring(end);
      jsonEditorTextarea.selectionStart = jsonEditorTextarea.selectionEnd = start + 2;
    }
  });

  btnLoadOpenmindDefault.addEventListener('click', () => {
    if (loadedConfigData && loadedConfigData.default_config) {
      try {
        const parsed = JSON.parse(loadedConfigData.default_config);
        jsonEditorTextarea.value = JSON.stringify(parsed, null, 2);
        updateModelDropdown(jsonEditorTextarea.value, 'openmind/zen-deepseek-v4-flash-free');
        jsonEditorError.classList.add('hidden');
        settingsStatus.textContent = 'Loaded OpenMind defaults from pibox:5000 spec';
        setTimeout(() => { settingsStatus.textContent = ''; }, 3000);
      } catch (e) {
        jsonEditorTextarea.value = loadedConfigData.default_config;
      }
    }
  });

  btnFormatJson.addEventListener('click', () => {
    try {
      const parsed = JSON.parse(jsonEditorTextarea.value);
      jsonEditorTextarea.value = JSON.stringify(parsed, null, 2);
      jsonEditorError.classList.add('hidden');
    } catch (err) {
      jsonEditorError.textContent = 'Format Error: ' + err.message;
      jsonEditorError.classList.remove('hidden');
    }
  });

  btnValidateJson.addEventListener('click', () => {
    try {
      JSON.parse(jsonEditorTextarea.value);
      jsonEditorError.classList.add('hidden');
      settingsStatus.textContent = '✓ JSON is valid!';
      setTimeout(() => { settingsStatus.textContent = ''; }, 3000);
    } catch (err) {
      jsonEditorError.textContent = 'Invalid JSON: ' + err.message;
      jsonEditorError.classList.remove('hidden');
    }
  });

  btnSaveSettings.addEventListener('click', async () => {
    const rawJson = jsonEditorTextarea.value.trim();
    if (rawJson) {
      try {
        JSON.parse(rawJson);
      } catch (err) {
        jsonEditorError.textContent = 'Syntax Error: ' + err.message;
        jsonEditorError.classList.remove('hidden');
        return;
      }
    }

    const selectedModel = settingsModelSelect.value;
    settingsStatus.textContent = 'Saving...';

    try {
      const res = await fetch(getApiUrl('api/config'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          config_json: rawJson,
          model: selectedModel
        })
      });

      if (res.ok) {
        const data = await res.json();
        if (data.active_model) {
          modelBadge.textContent = data.active_model;
        }
        settingsStatus.textContent = 'Saved successfully!';
        setTimeout(() => {
          closeSettingsModal();
          loadInfo();
        }, 600);
      } else {
        const errText = await res.text();
        jsonEditorError.textContent = 'Save error: ' + errText;
        jsonEditorError.classList.remove('hidden');
        settingsStatus.textContent = '';
      }
    } catch (err) {
      jsonEditorError.textContent = 'Request error: ' + err.message;
      jsonEditorError.classList.remove('hidden');
      settingsStatus.textContent = '';
    }
  });

  if (btnSyncModels) btnSyncModels.addEventListener('click', triggerSyncModels);
  if (optSyncModels) optSyncModels.addEventListener('click', triggerSyncModels);

  btnSettings.addEventListener('click', openSettingsModal);
  if (optSettings) optSettings.addEventListener('click', openSettingsModal);
  modalClose.addEventListener('click', closeSettingsModal);
  btnCancelSettings.addEventListener('click', closeSettingsModal);

  settingsModal.addEventListener('click', (e) => {
    if (e.target === settingsModal) closeSettingsModal();
  });

  document.addEventListener('click', (e) => {
    if (sessionDropdownWrap && !sessionDropdownWrap.contains(e.target)) {
      if (sessionDropdownMenu) sessionDropdownMenu.classList.add('hidden');
      if (sessionDropdownWrap) sessionDropdownWrap.classList.remove('open');
    }
    if (e.target && e.target.classList.contains('model-pill-btn')) {
      const targetModel = e.target.getAttribute('data-model');
      if (targetModel) swapModel(targetModel);
    }
  });
})();
