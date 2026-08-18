// The corvex UI. Vanilla on purpose: this file is embedded in a binary a user
// runs with their own credentials, and a build step plus a dependency tree is
// surface nobody can audit at the moment they need to trust it.
//
// The screens are the canvas's: the inbox is 2a, the gate detail with its
// reading lock is 2b, dispatch is 2c, a run is 2d/2f, the history is 2e, and the
// command log is 2h. Every mutating action goes through the same API the CLI
// calls, and the server records the CLI equivalent — which is what the ⌘K panel
// shows.

const $ = (sel, root = document) => root.querySelector(sel);
const el = (tag, attrs = {}, ...kids) => {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else if (v !== null && v !== undefined) node.setAttribute(k, v);
  }
  for (const kid of kids.flat()) if (kid) node.append(kid.nodeType ? kid : document.createTextNode(kid));
  return node;
};

const api = {
  async get(path) {
    const r = await fetch(path, { headers: { Accept: 'application/json' } });
    if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || `HTTP ${r.status}`);
    return r.json();
  },
  async post(path, body) {
    const r = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body || {}),
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) throw Object.assign(new Error(data.error || `HTTP ${r.status}`), { data });
    return data;
  },
};

const state = { view: 'inbox', detail: null, data: null, timer: null };

function status(msg, kind = '') {
  const bar = $('#status');
  bar.textContent = msg || '';
  bar.style.color = kind === 'bad' ? 'var(--bad)' : kind === 'ok' ? 'var(--ok)' : 'var(--dim)';
}

// ── time ────────────────────────────────────────────────────────────────────
// A gate parked for three days has to read as three days. Nanoseconds come from
// Go's time.Duration, which is what the API already carries.
function human(ns) {
  const s = Math.max(0, Math.round(Number(ns) / 1e9));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.round(s / 60)}m`;
  if (s < 172800) return `${Math.round(s / 3600)}h`;
  return `${Math.round(s / 86400)}d`;
}
const money = (v) => `$${(Number(v) || 0).toFixed(2)}`;

// A step that an agent wrote and a step a script ran are the product's central
// distinction, so they never look alike: inference is purple, determinism green.
function kindPill(kind) {
  const ai = kind === 'code' || kind === 'inferential' || kind === 'human';
  return el('span', { class: `pill ${ai ? 'ai' : 'det'}`, text: kind });
}

// ── inbox (2a) ──────────────────────────────────────────────────────────────
function renderInbox(root, data) {
  const gates = data.inbox.gates || [];
  const escalations = data.inbox.escalations || [];
  $('#badge-inbox').textContent = gates.length + escalations.length || '';

  if (!gates.length && !escalations.length) {
    root.append(el('p', { class: 'empty', text: 'Nothing is waiting on you.' }));
  }
  if (gates.length) root.append(el('h2', { text: `${gates.length} gate(s) waiting` }));
  for (const g of gates) {
    root.append(el('div', { class: 'card waiting' },
      el('div', { class: 'row' },
        el('span', { class: 'mono-id', text: g.gate.run_id }),
        kindPill(g.gate.nature),
        el('span', { class: 'grow', text: g.gate.label || g.gate.title || '' }),
        el('span', { class: 'pill warn', text: `waiting ${human(g.waiting_ns)}` }),
        el('button', { class: 'primary', onclick: () => openGate(g.gate.run_id, g.gate.step_id) }, 'Review'),
      ),
      el('div', { class: 'dim', text: `step ${g.gate.step_id} · ${g.gate.recipe || g.gate.project || ''} · run is ${g.liveness}` }),
    ));
  }
  if (escalations.length) root.append(el('h2', { text: `${escalations.length} escalation(s)` }));
  for (const e of escalations) {
    root.append(el('div', { class: 'card waiting' },
      el('div', { class: 'row' },
        el('span', { class: 'mono-id', text: e.project }),
        el('span', { class: 'pill', text: `step ${e.step}` }),
        el('span', { class: 'grow dim', text: (e.head || [])[0] || '' }),
        el('span', { class: 'pill warn', text: `waiting ${human(e.waiting_ns)}` }),
      ),
      el('div', { class: 'dim', text: `corvex gate show ${e.project} --step ${e.step}` }),
    ));
  }

  const live = (data.runs || []).filter((r) => r.liveness === 'alive' || r.liveness === 'canceling');
  if (live.length) {
    root.append(el('h2', { text: 'running now' }));
    for (const r of live) root.append(runCard(r));
  }
}

// ── runs (2e) ───────────────────────────────────────────────────────────────
function runCard(r) {
  const cls = r.status === 'failed' ? 'bad' : r.status === 'parked' ? 'warn' : '';
  return el('div', { class: 'card' },
    el('div', { class: 'row' },
      el('span', { class: 'mono-id', text: r.run_id }),
      el('span', { class: `pill ${cls}`, text: r.status }),
      el('span', { class: 'pill', text: r.liveness }),
      r.environment && r.environment !== 'simple' ? el('span', { class: 'pill warn', text: r.environment }) : null,
      el('span', { class: 'grow', text: r.recipe || r.project || '' }),
      el('span', { class: 'dim', text: `${human(r.age_ns)} ago` }),
      el('button', { onclick: () => openRun(r.run_id) }, 'Open'),
      r.liveness === 'alive' ? el('button', { class: 'danger', onclick: () => killRun(r.run_id) }, 'Stop') : null,
    ),
    el('div', { class: 'dim', text: r.repo }),
  );
}

function renderRuns(root, data) {
  root.append(el('div', { class: 'row' },
    el('h2', { class: 'grow', text: 'history' }),
    el('button', { class: 'primary', onclick: dispatchForm }, 'Dispatch a run'),
  ));
  const runs = data.runs || [];
  if (!runs.length) root.append(el('p', { class: 'empty', text: 'No runs in the last 7 days.' }));
  for (const r of runs) root.append(runCard(r));
}

// ── dispatch (2c) ───────────────────────────────────────────────────────────
async function dispatchForm() {
  const recipes = await api.get('/api/recipes').catch(() => []);
  const target = el('input', { placeholder: 'recipe or project', list: 'recipe-list' });
  const env = el('select', {}, el('option', { value: 'simple' }, 'simple'), el('option', { value: 'stack' }, 'stack (database)'));
  const box = el('div', { class: 'card' },
    el('div', { class: 'row' },
      target,
      el('datalist', { id: 'recipe-list' }, (recipes || []).map((r) => el('option', { value: r.name }))),
      env,
      el('button', {
        class: 'primary',
        onclick: async () => {
          try {
            const res = await api.post('/api/runs', { target: target.value.trim(), environment: env.value });
            status(`started — ${res.command}`, 'ok');
            refresh();
          } catch (e) { status(e.message, 'bad'); }
        },
      }, 'Run'),
    ),
    el('div', { class: 'dim', text: 'The run is detached: closing this page does not stop it.' }),
  );
  const view = $('#view');
  view.prepend(box);
}

// ── run detail (2d/2f) ──────────────────────────────────────────────────────
async function openRun(id) {
  state.detail = { kind: 'run', id };
  render();
}

async function renderRunDetail(root, id) {
  const r = await api.get(`/api/runs/${encodeURIComponent(id)}`);
  root.append(el('div', { class: 'row' },
    el('button', { class: 'ghost', onclick: () => { state.detail = null; render(); } }, '← back'),
    el('span', { class: 'mono-id', text: r.run_id || r.project }),
    el('span', { class: 'pill', text: r.status || 'never run' }),
    r.environment && r.environment !== 'simple' ? el('span', { class: 'pill warn', text: r.environment }) : null,
    el('span', { class: 'grow dim', text: r.repo }),
    el('span', { class: 'dim', text: `${r.completed}/${r.total} steps · ${money(r.cost_usd)}` }),
  ));
  // Where the money went, by the nature of the work that spent it (2f), and the
  // human clock kept apart from the run's clock (2g). Both only exist because
  // F5 started writing `phase` — a run from before that shows one bucket called
  // `unattributed`, which is the honest answer rather than a guess.
  if ((r.per_phase || []).length) {
    const bars = el('div', { class: 'card' },
      el('div', { class: 'row' },
        el('strong', { class: 'grow', text: 'cost by nature' }),
        r.human_wait_ms ? el('span', { class: 'pill warn', text: `${human(r.human_wait_ms * 1e6)} waiting on a person` }) : null,
      ),
    );
    for (const p of r.per_phase) {
      const share = r.cost_usd > 0 ? Math.max(2, Math.round((p.cost_usd / r.cost_usd) * 100)) : 0;
      bars.append(el('div', { class: 'row' },
        el('span', { class: 'id', text: p.phase, style: 'width:12ch' }),
        el('span', { class: 'dim', text: money(p.cost_usd), style: 'width:8ch' }),
        el('span', { class: 'bar', style: `width:${share}%` }),
      ));
    }
    root.append(bars);
  }

  const steps = el('div', { class: 'steps' });
  for (const t of r.tasks || []) {
    steps.append(el('div', { class: 'step' },
      el('span', { class: 'id', text: t.id }),
      el('span', { class: `pill ${t.status === 'FAILED' ? 'bad' : t.status === 'PASSED' ? 'det' : ''}`, text: t.status }),
      el('span', { class: 'grow', text: t.title }),
      t.retries ? el('span', { class: 'pill warn', text: `${t.retries} retr${t.retries === 1 ? 'y' : 'ies'}` }) : null,
      t.duration_ms ? el('span', { class: 'dim', text: human(t.duration_ms * 1e6) }) : null,
      t.cost_usd ? el('span', { class: 'dim', text: money(t.cost_usd) }) : null,
    ));
  }
  root.append(el('div', { class: 'card' }, steps));
}

async function killRun(id) {
  if (!confirm(`Stop ${id}? It writes 'canceling' and unwinds.`)) return;
  status('proving the pid still belongs to this run…');
  try {
    const res = await api.post(`/api/runs/${encodeURIComponent(id)}/kill`);
    status(`stopped — ${res.action.command}`, 'ok');
  } catch (e) { status(e.message, 'bad'); }
  refresh();
}

// ── gate detail (2b) — the screen the whole product exists for ───────────────
async function openGate(runID, step) {
  state.detail = { kind: 'gate', id: runID, step };
  render();
}

async function renderGateDetail(root, id, step) {
  const view = await api.get(`/api/gates/${encodeURIComponent(id)}?step=${encodeURIComponent(step || '')}`);
  const g = view.gate;
  const required = (g.evidence || []).filter((e) => e.required_reading).map((e) => e.label);
  const read = new Set();

  const approve = el('button', { class: 'primary', disabled: required.length ? '' : null }, 'Approve');
  const relock = () => { approve.toggleAttribute('disabled', read.size < required.length); };

  root.append(el('div', { class: 'row' },
    el('button', { class: 'ghost', onclick: () => { state.detail = null; render(); } }, '← back'),
    el('span', { class: 'mono-id', text: g.run_id }),
    kindPill(g.nature),
    el('span', { class: 'grow', text: g.label || g.title }),
    el('span', { class: 'pill warn', text: `waiting ${human(view.waiting_ns)}` }),
    el('span', { class: 'pill', text: `run is ${view.liveness}` }),
  ));
  if (g.prompt) root.append(el('div', { class: 'card' }, g.prompt));

  for (const e of g.evidence || []) {
    const body = el('pre', { text: e.content || '' });
    const head = el('header', { class: 'row' },
      el('strong', { text: e.label }),
      el('span', { class: 'pill', text: e.kind }),
      e.status ? el('span', { class: `pill ${e.status === 'fail' ? 'bad' : e.status === 'warn' ? 'warn' : 'det'}`, text: e.status }) : null,
      e.truncated ? el('span', { class: 'pill warn', text: 'truncated' }) : null,
      el('span', { class: 'grow' }),
      e.required_reading
        ? el('label', { class: 'read' },
            el('input', {
              type: 'checkbox',
              onchange: (ev) => { ev.target.checked ? read.add(e.label) : read.delete(e.label); relock(); },
            }),
            el('span', { class: 'required', text: 'I read this' }))
        : null,
    );
    root.append(el('div', { class: 'evidence' }, head, body));
  }

  const reason = el('input', { placeholder: 'why (optional)', class: 'grow' });
  approve.addEventListener('click', async () => {
    try {
      const res = await api.post(`/api/gates/${encodeURIComponent(g.run_id)}/approve`, { step: g.step_id, ack: [...read] });
      status(`approved — ${res.action.command}`, 'ok');
      state.detail = null; refresh();
    } catch (e) { status(e.message, 'bad'); }
  });
  root.append(el('div', { class: 'card' },
    el('div', { class: 'row' },
      reason,
      el('button', {
        class: 'danger',
        onclick: async () => {
          try {
            const res = await api.post(`/api/gates/${encodeURIComponent(g.run_id)}/reject`, { step: g.step_id, reason: reason.value });
            status(`rejected — ${res.action.command}`, 'ok');
            state.detail = null; refresh();
          } catch (e) { status(e.message, 'bad'); }
        },
      }, 'Reject'),
      approve,
    ),
    required.length
      ? el('div', { class: 'dim', text: `Approval unlocks after reading: ${required.join(', ')}. The same lock exists on the CLI as --ack.` })
      : null,
  ));
  relock();
}

// ── recipes ─────────────────────────────────────────────────────────────────
async function renderRecipes(root) {
  const list = await api.get('/api/recipes');
  if (!list || !list.length) root.append(el('p', { class: 'empty', text: 'No recipes in .corvex/recipes/.' }));
  for (const r of list || []) {
    root.append(el('div', { class: 'card' },
      el('div', { class: 'row' },
        el('span', { class: 'grow', text: r.name }),
        r.problem ? el('span', { class: 'pill bad', text: 'broken' }) : el('span', { class: 'pill', text: `${r.stages} stage(s)` }),
        r.gates ? el('span', { class: 'pill ai', text: `${r.gates} gate(s)` }) : null,
        r.compiled ? el('span', { class: 'pill det', text: 'compiled' }) : null,
      ),
      el('div', { class: 'dim', text: r.problem || r.description || '' }),
    ));
  }
}

// ── ⌘K: the parity log (2h) ─────────────────────────────────────────────────
async function openPalette() {
  const list = $('#palette-list');
  list.replaceChildren();
  const actions = await api.get('/api/actions?limit=50').catch(() => []);
  if (!actions || !actions.length) list.append(el('li', { class: 'dim' }, 'Nothing yet. Every action you take here shows up as the command that does the same thing.'));
  for (const a of actions || []) {
    list.append(el('li', {},
      el('span', { class: a.result === 'ok' ? 'pill det' : 'pill bad', text: a.result === 'ok' ? 'ok' : 'failed' }),
      el('code', { class: 'grow', text: a.command }),
      el('span', { class: 'dim', text: new Date(a.at).toLocaleTimeString() }),
    ));
  }
  $('#palette').classList.remove('hidden');
}

// ── shell ───────────────────────────────────────────────────────────────────
async function render() {
  const root = $('#view');
  root.replaceChildren();
  try {
    if (state.detail?.kind === 'run') return await renderRunDetail(root, state.detail.id);
    if (state.detail?.kind === 'gate') return await renderGateDetail(root, state.detail.id, state.detail.step);
    if (state.view === 'recipes') return await renderRecipes(root);
    const data = state.data || (await api.get('/api/state'));
    $('#repo').textContent = data.repo || '';
    if (state.view === 'runs') return renderRuns(root, data);
    return renderInbox(root, data);
  } catch (e) {
    root.append(el('p', { class: 'empty', text: String(e.message || e) }));
  }
}

async function refresh() {
  try {
    state.data = await api.get('/api/state');
    await render();
  } catch (e) { status(e.message, 'bad'); }
}

function boot() {
  for (const tab of document.querySelectorAll('.tab')) {
    tab.addEventListener('click', () => {
      state.view = tab.dataset.view;
      state.detail = null;
      for (const t of document.querySelectorAll('.tab')) t.classList.toggle('active', t === tab);
      render();
    });
  }
  document.querySelector('.tab').classList.add('active');
  $('#palette-open').addEventListener('click', openPalette);
  $('#palette-close').addEventListener('click', () => $('#palette').classList.add('hidden'));
  document.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); openPalette(); }
    if (e.key === 'Escape') $('#palette').classList.add('hidden');
  });
  refresh();
  // Poll rather than stream: the run's own ledger and record are the only
  // cross-process channel that exists today (F1/F2), and a five-second poll of
  // three files is cheaper than the SSE plumbing it would replace. Registered as
  // a debt rather than pretended away.
  state.timer = setInterval(() => { if (!state.detail) refresh(); }, 5000);
}

boot();
