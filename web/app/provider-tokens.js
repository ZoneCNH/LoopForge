// GitLab Self-Managed 等 Provider API 凭据管理：一个实例一条 token，
// 列表只展示描述与脱敏 hint，secret 不回显。
let providerTokenCache = null;

function providerTokenUserId() {
  const id = state.currentUser?.id;
  return id && !state.currentUser.isGuest ? id : null;
}

function loadProviderTokens(refresh = false) {
  const userId = providerTokenUserId();
  if (!userId) return Promise.resolve([]);
  if (!providerTokenCache || providerTokenCache.userId !== userId) providerTokenCache = {userId, tokens:null, pending:null};
  const cache = providerTokenCache;
  if (cache.pending) return cache.pending;
  if (!refresh && cache.tokens) return Promise.resolve(cache.tokens);
  cache.pending = api(`/api/users/${encodeURIComponent(userId)}/provider-tokens`, {silent:true})
    .then(items => cache.tokens = Array.isArray(items) ? items : [])
    .finally(() => { cache.pending = null; });
  return cache.pending;
}

function providerTokenStatusText(status) {
  return {active:'有效', auth_failed:'校验失败', revoked:'已撤销'}[status] || status || '未知';
}

function openProviderTokens(prefillInstance = '') {
  const userId = providerTokenUserId();
  if (!userId) return;

  const previous = $('#providerTokenDialog');
  if (previous?.open) return;
  const opener = document.activeElement;
  const endpoint = `/api/users/${encodeURIComponent(userId)}/provider-tokens`;
  let tokens = [];
  let editing = null;
  let deleting = '';
  let busy = false;

  const dialog = document.createElement('dialog');
  dialog.id = 'providerTokenDialog';
  dialog.className = 'git-ssh-dialog';
  dialog.setAttribute('aria-labelledby', 'providerTokenTitle');
  dialog.innerHTML = `
    <header class="git-ssh-dialog-head">
      <h2 id="providerTokenTitle">GitLab 实例 Token</h2>
      <button type="button" class="credential-btn ghost icon" data-pt-close aria-label="关闭">${credentialGlyph('close', 16)}</button>
    </header>
    <p class="git-ssh-description">每个 GitLab Self-Managed 实例配置一条 Access Token；Issue、MR 与流水线状态通过该实例 API 同步。</p>
    <p class="git-ssh-status" role="status">正在读取凭据…</p>
    <div class="git-ssh-key-list"></div>
    <button type="button" class="credential-btn primary" data-pt-add disabled>添加 Token</button>
    <form class="git-ssh-key-form" hidden>
      <h3></h3>
      ${credentialFieldHTML({label:'实例地址', name:'instanceKey', type:'text', placeholder:'https://gitlab.example.com', hint:'GitLab 实例的 http(s) 基础地址，不含路径。'})}
      <label class="credential-field"><span>凭据类型</span><div class="credential-input is-plain"><select name="credentialType" autocomplete="off"><option value="pat">Personal Access Token</option><option value="project_token">Project Access Token</option><option value="group_token">Group Access Token</option></select></div></label>
      ${credentialFieldHTML({label:'Access Token', name:'token', type:'password', placeholder:'粘贴 GitLab Access Token', hint:'需要 api scope；保存后不再回显。'})}
      ${credentialFieldHTML({label:'备注（可选）', name:'scopesSummary', type:'text', placeholder:'例如 read_api + api', hint:null})}
      <footer class="git-ssh-form-actions">
        <button type="button" class="credential-btn secondary" data-pt-cancel>取消</button>
        <button type="submit" class="credential-btn primary">保存 Token</button>
      </footer>
    </form>`;

  const list = dialog.querySelector('.git-ssh-key-list');
  const status = dialog.querySelector('.git-ssh-status');
  const add = dialog.querySelector('[data-pt-add]');
  const form = dialog.querySelector('form');
  const instanceKey = form.elements.namedItem('instanceKey');
  const credentialType = form.elements.namedItem('credentialType');
  const token = form.elements.namedItem('token');
  const scopesSummary = form.elements.namedItem('scopesSummary');
  instanceKey.maxLength = 2048;
  token.maxLength = 512;
  scopesSummary.maxLength = 500;
  instanceKey.required = true;

  function setStatus(message = '', error = false) {
    status.textContent = message;
    status.classList.toggle('is-error', error);
    status.hidden = !message;
  }

  function renderList() {
    list.innerHTML = tokens.map(item => {
      const id = escapeHTML(item.id);
      const stateText = providerTokenStatusText(item.status);
      const meta = [item.credentialType, item.tokenHint ? `…${escapeHTML(item.tokenHint)}` : '', item.lastValidatedAt ? `上次校验 ${escapeHTML(String(item.lastValidatedAt).slice(0,10))}` : ''].filter(Boolean).join(' · ');
      return `<div class="git-ssh-key-row">
        <div class="git-ssh-key-summary"><b>${escapeHTML(item.instanceKey)}</b><small>${meta} · <span class="${item.status==='active'?'':'is-error'}">${stateText}</span></small></div>
        ${deleting === item.id
          ? `<span>删除这条凭据？</span><button type="button" class="credential-btn ghost" data-pt-keep>取消</button><button type="button" class="credential-btn danger-solid" data-pt-remove="${id}">确认删除</button>`
          : `<button type="button" class="credential-btn ghost" data-pt-validate="${id}">校验</button><button type="button" class="credential-btn ghost" data-pt-edit="${id}">替换</button><button type="button" class="credential-btn ghost" data-pt-delete="${id}">删除</button>`}
      </div>`;
    }).join('');
  }

  function openForm(item) {
    editing = item || null;
    deleting = '';
    renderList();
    form.reset();
    form.hidden = false;
    add.hidden = true;
    form.querySelector('h3').textContent = item ? '替换 Token' : '添加 Token';
    instanceKey.value = item?.instanceKey || '';
    instanceKey.readOnly = !!item;
    credentialType.value = item?.credentialType || 'pat';
    token.required = !item;
    token.placeholder = item ? '留空保留现有 Token' : '粘贴 GitLab Access Token';
    scopesSummary.value = item?.scopesSummary || '';
    (item ? token : instanceKey).focus();
  }

  function closeForm() {
    form.reset();
    form.hidden = true;
    add.hidden = false;
    editing = null;
    add.focus();
  }

  async function request(method, body, id) {
    if (busy) return;
    busy = true;
    dialog.setAttribute('aria-busy', 'true');
    const save = form.querySelector('button[type="submit"]');
    save.disabled = true;
    setStatus(method === 'PUT' ? '正在保存…' : '正在删除…');
    try {
      const result = await api(id ? `${endpoint}/${encodeURIComponent(id)}` : endpoint, {
        method, silent:true, ...(body ? {body:JSON.stringify(body)} : {}),
      });
      tokens = Array.isArray(result) ? result : tokens;
      providerTokenCache = {userId, tokens, pending:null};
      deleting = '';
      renderList();
      if (method === 'PUT') closeForm();
      setStatus(method === 'PUT' ? 'Token 已保存' : '凭据已删除');
    } catch (error) {
      setStatus(error.message, true);
    } finally {
      busy = false;
      save.disabled = false;
      dialog.removeAttribute('aria-busy');
    }
  }

  async function validate(item) {
    if (busy) return;
    busy = true;
    setStatus('正在校验…');
    try {
      const result = await api(`${endpoint}/validate`, {
        method:'POST', silent:true,
        body:JSON.stringify({providerKind:'gitlab', instanceKey:item.instanceKey}),
      });
      if (result?.valid) {
        item.status = 'active';
        item.lastValidatedAt = new Date().toISOString();
        setStatus(`校验通过${result.login ? `（${result.login}）` : ''}`);
      } else {
        item.status = 'auth_failed';
        setStatus(result?.error || '校验失败', true);
      }
      providerTokenCache = {userId, tokens, pending:null};
      renderList();
    } catch (error) {
      setStatus(error.message, true);
    } finally {
      busy = false;
    }
  }

  dialog.onclick = event => {
    const button = event.target.closest('button');
    if (!button || busy) return;
    const action = button.dataset;
    if ('ptClose' in action) return dialog.close();
    if ('ptCancel' in action) return closeForm();
    if ('ptAdd' in action) return openForm(null);
    const item = tokens.find(t => t.id === (action.ptEdit || action.ptDelete || action.ptRemove || action.ptValidate));
    if (action.ptEdit) return openForm(item);
    if (action.ptRemove) return request('DELETE', null, action.ptRemove);
    if (action.ptValidate) return validate(item);
    if (action.ptDelete || 'ptKeep' in action) {
      deleting = action.ptDelete || '';
      renderList();
      const confirmation = list.querySelector('[data-pt-remove]');
      (confirmation || add).focus();
    }
  };
  form.onsubmit = event => {
    event.preventDefault();
    return request('PUT', {
      providerKind:'gitlab',
      instanceKey:instanceKey.value,
      credentialType:credentialType.value,
      token:token.value,
      scopesSummary:scopesSummary.value,
    });
  };
  dialog.addEventListener('keydown', event => {
    if (event.key === 'Escape' || event.metaKey || event.ctrlKey) event.stopPropagation();
  });
  dialog.addEventListener('cancel', event => { if (busy) event.preventDefault(); });
  dialog.addEventListener('close', () => {
    form.reset();
    dialog.remove();
    if (opener?.isConnected) opener.focus({preventScroll:true});
  });
  document.body.append(dialog);
  dialog.showModal();
  hydrateCredentialIcons(dialog);
  loadProviderTokens(true).then(items => {
    if (!dialog.open) return;
    tokens = items;
    add.disabled = false;
    setStatus(tokens.length ? '' : '还没有配置 GitLab 实例 Token');
    renderList();
    if (prefillInstance) {
      const existing = tokens.find(t => t.instanceKey === prefillInstance);
      openForm(existing || null);
      if (!existing) instanceKey.value = prefillInstance;
    }
  }).catch(error => setStatus(error.message, true));
}
