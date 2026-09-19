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
    // `style` takes an object and goes through the CSSOM, never through a style
    // ATTRIBUTE: the server sends `style-src 'self'` (csp.go), and an inline
    // style attribute would need 'unsafe-inline' — which also re-opens the
    // injection hole the policy exists to close. A property assignment is not
    // an inline style, so this is the shape every caller has to use.
    //
    // The type check exists because the two ways to get this wrong fail in the
    // two least useful ways, and neither of them says `style`. MEASURED, not
    // assumed (e2e/ui_dom_test.go pins both):
    //
    //   `style: 'width: 12ch'` — the mistake somebody writes out of habit —
    //   throws, but from the wrong layer: "Failed to set an indexed property [0]
    //   on 'CSSStyleDeclaration'". Object.assign is walking the characters of
    //   the string. That aborts a render with a message about an indexed
    //   property setter, naming neither the attribute nor the element.
    //
    //   `style: null` or a number throws nothing at all and applies nothing —
    //   Object.assign ignores a primitive source. A bar at zero width with no
    //   thread to pull.
    //
    // One line turns both into a sentence that names the contract it broke.
    else if (k === 'style') {
      if (typeof v !== 'object' || v === null) {
        throw new TypeError(`el(${tag}): style takes an object of CSS properties, got ${typeof v}`);
      }
      Object.assign(node.style, v);
    }
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

const state = { view: 'inbox', detail: null, data: null, timer: null, stream: null, streamAt: 0, fingerprint: '' };

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

// ── what is waiting, said OUTSIDE this tab ──────────────────────────────────
//
// The tool's whole promise is that a run goes on without you and stops when it
// needs you. A gate that opens while the browser is behind an editor then waits
// for a human eye to wander back — which is the same as the runner not having
// told anybody.
//
// Two channels, deliberately different in cost:
//
//   · the TITLE always carries the count. It needs no permission, it survives a
//     denied notification prompt, and a tab strip is where a person already
//     looks for "is something waiting".
//   · a system notification fires only when the count GOES UP and only while the
//     page is hidden. Not on every render (a re-read is not news) and not while
//     the person is looking at the inbox (they can see it).
//
// Permission is asked on a CLICK and never on load: a page that opens with a
// permission prompt gets it denied once and for all, and takes the title badge
// down with it in the user's memory of "that tool that nags".
let lastWaiting = null;

function announceWaiting(count, items) {
  document.title = count > 0 ? `(${count}) corvex` : 'corvex';
  const previous = lastWaiting;
  lastWaiting = count;
  if (previous === null || count <= previous || !document.hidden) return;
  if (typeof Notification !== 'function' || Notification.permission !== 'granted') return;
  const newest = items[0];
  try {
    new Notification(`corvex: ${count} esperando você`, {
      body: newest || 'um gate abriu',
      tag: 'corvex-inbox', // one notification, replaced — not a pile
    });
  } catch (_) {
    // A browser that refuses to construct it (some do while the page is
    // backgrounded) still leaves the title badge, which is the channel that
    // never fails.
  }
}

// ── inbox (2a) ──────────────────────────────────────────────────────────────
function renderInbox(root, data) {
  const gates = data.inbox.gates || [];
  const escalations = data.inbox.escalations || [];
  // A dispatch that died needs a person exactly the way a parked gate does: the
  // run they asked for is not running, and nothing else on the screen says so.
  const dead = (data.dispatches || []).filter((d) => d.result && d.result !== 'ok');
  $('#badge-inbox').textContent = gates.length + escalations.length + dead.length || '';

  if (!gates.length && !escalations.length && !dead.length) {
    root.append(el('p', { class: 'empty', text: 'Nothing is waiting on you.' }));
  }
  renderDeadDispatches(root, data);
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
  // `partial` reads as a warning, not as an ending: the recipe did not finish,
  // and the line somebody scans has to carry that.
  const cls = r.status === 'failed' ? 'bad'
    : r.status === 'parked' || r.status === 'partial' ? 'warn' : '';
  return el('div', { class: 'card' },
    el('div', { class: 'row' },
      el('span', { class: 'mono-id', text: r.run_id }),
      el('span', { class: `pill ${cls}`, text: r.status }),
      el('span', { class: 'pill', text: r.liveness }),
      r.environment && r.environment !== 'simple' ? el('span', { class: 'pill warn', text: r.environment }) : null,
      el('span', { class: 'grow', text: r.recipe || r.project || '' }),
      // What it spent, on the LIST. A screen where a $0.40 run and a $23 run
      // look identical until you open them cannot answer the question people
      // actually have about an agent runner. The number is the same one the run
      // screen shows — one rule, in ops, so the two cannot drift.
      r.cost_usd ? el('span', { class: 'pill', text: money(r.cost_usd) }) : null,
      el('span', { class: 'dim', text: `${human(r.age_ns)} ago` }),
      el('button', { onclick: () => openRun(r.run_id) }, 'Open'),
      // Two controls, and they are not the same control: pause holds the run at
      // its next wave and keeps the work, stop signals it and loses whatever is
      // in flight. A run reporting `paused` offers the way back instead.
      r.status === 'paused' ? el('button', { onclick: () => resumeRun(r.run_id) }, 'Resume')
        : r.liveness === 'alive' ? el('button', { onclick: () => pauseRun(r.run_id) }, 'Pause') : null,
      r.liveness === 'alive' ? el('button', { class: 'danger', onclick: () => killRun(r.run_id) }, 'Stop') : null,
    ),
    // Which repository this run belongs to, by name, because the point of one
    // screen over several repositories is that a row says which one it is
    // without the reader parsing a path.
    el('div', { class: 'dim' },
      el('span', { class: 'pill', text: (r.repo || '').split('/').filter(Boolean).pop() || '?' }),
      ' ',
      r.repo || '',
    ),
  );
}

function renderRuns(root, data) {
  root.append(el('div', { class: 'row' },
    el('h2', { class: 'grow', text: 'history' }),
    el('button', { class: 'primary', onclick: dispatchForm }, 'Dispatch a run'),
  ));
  renderDeadDispatches(root, data);
  const runs = data.runs || [];
  if (!runs.length) root.append(el('p', { class: 'empty', text: 'No runs in the last 7 days.' }));
  for (const r of runs) root.append(runCard(r));
}

// ── dispatches that never became runs ───────────────────────────────────────
// A run that dies before it registers itself leaves NOTHING in the history: no
// row, no id, no cost. The screen said `ok` at dispatch and then showed an empty
// list, and the reason — a dirty tree, a worktree the run belonged in, a recipe
// that does not parse — was in a file on disk nobody mentioned.
//
// So the failed dispatch is a card of its own, above the history, with the exit
// status the server recorded and a button that opens the log. It disappears on
// its own: it is drawn from the action log, which is bounded by the same window
// as everything else on this screen.
function renderDeadDispatches(root, data) {
  const dead = (data.dispatches || []).filter((d) => d.result && d.result !== 'ok');
  if (!dead.length) return;
  root.append(el('h2', { text: `${dead.length} dispatch(es) that never started` }));
  for (const d of dead) {
    root.append(el('div', { class: 'card bad' },
      el('div', { class: 'row' },
        el('span', { class: 'pill bad', text: 'died' }),
        el('code', { class: 'grow', text: d.command }),
        el('span', { class: 'dim', text: new Date(d.at).toLocaleTimeString() }),
        d.log ? el('button', { onclick: () => openLog(d.log) }, 'Log') : null,
      ),
      el('div', { class: 'dim', text: d.result }),
    ));
  }
}

// ── a dispatch log (the other half of 2h) ───────────────────────────────────
async function openLog(name) {
  state.detail = { kind: 'log', name };
  render();
}

async function renderLogDetail(root, name) {
  const log = await api.get(`/api/runs/logs/${encodeURIComponent(name)}`);
  root.append(el('div', { class: 'row' },
    el('button', { onclick: leaveDetail }, '← back'),
    el('h2', { class: 'grow', text: name }),
    log.truncated ? el('span', { class: 'pill warn', text: 'tail only' }) : null,
  ));
  root.append(el('div', { class: 'dim', text: log.path }));
  root.append(el('pre', { class: 'log', text: log.content || '(empty)' }));
}

// ── dispatch (2c) ─────────────────────────────────────────────
// The form is the whole flow, in the order a person does it: which repository,
// which checkout (or make one), which recipe, what the recipe needs to know.
//
// The checkout half used to be missing, and its absence was the seam where the
// tool stopped being usable from the screen: the operating model is one worktree
// per piece of work, so every run started with `git worktree add -b … main` in a
// terminal, and only then did the UI have anywhere to dispatch into.
async function dispatchForm() {
  // The repository comes FIRST, because everything under it depends on which one
  // is meant: which recipes exist, which worktree a project has, which checkout
  // the run writes into. The list is the one the inbox already aggregates —
  // this repo plus the ones the run index knows — so the form can never offer a
  // repository the screen would refuse to dispatch into.
  const repos = await api.get('/api/repos').catch(() => []);
  const current = (repos || []).find((r) => r.current) || { path: '' };
  const repo = el('select', {}, ...(repos || []).map((r) =>
    el('option', { value: r.path, selected: r.current ? 'selected' : null }, r.name)));
  const repoPath = () => repo.value || current.path;

  // The checkout the run happens in. It defaults to the repository itself, which
  // keeps the old behaviour intact: a project with a `<repo>-<project>` worktree
  // is still redirected into it by the server.
  const worktree = el('select', {});
  const loadWorktrees = async () => {
    const list = await api.get(`/api/worktrees?repo=${encodeURIComponent(repoPath())}`).catch(() => []);
    worktree.replaceChildren(...(list || []).map((w) =>
      el('option', { value: w.path },
        w.main ? `${w.branch || 'detached'} (main checkout)` : `${w.branch || 'detached'} — ${w.path.split('/').pop()}`)));
  };

  // The recipes are read from the CHECKOUT, not from the repository.
  //
  // A worktree is the repository on another branch, and `.corvex/recipes` is
  // tracked: the checkout where the run happens can carry recipes the main one
  // has never seen. Reading the catalogue from the repository would offer a list
  // that does not match what will execute, and — because the declared inputs
  // come from that same catalogue — would silently stop asking for the values
  // the recipe needs.
  const recipeList = el('datalist', { id: 'recipe-list' });
  let recipes = [];
  const loadRecipes = async () => {
    const where = worktree.value || repoPath();
    recipes = await api.get(`/api/recipes?repo=${encodeURIComponent(where)}`).catch(() => []) || [];
    recipeList.replaceChildren(...recipes.map((r) => el('option', { value: r.name })));
    syncInputs();
  };
  worktree.addEventListener('change', loadRecipes);
  repo.addEventListener('change', async () => { await loadWorktrees(); loadRecipes(); });

  const target = el('input', { placeholder: 'recipe or project', list: 'recipe-list' });
  // What the recipe says it needs (`requires: - env:`), asked for here instead of
  // being discovered as a preflight failure in a log. The fields are rebuilt
  // whenever the recipe changes, so switching recipes cannot leave the previous
  // one's values behind to be posted with the next.
  const inputsBox = el('div', { class: 'inputs' });
  const inputFields = new Map();
  function syncInputs() {
    const r = recipes.find((x) => x.name === target.value.trim());
    inputFields.clear();
    if (!r || !(r.inputs || []).length) { inputsBox.replaceChildren(); return; }
    inputsBox.replaceChildren(...r.inputs.map((inp) => {
      const field = el('input', { placeholder: inp.name });
      inputFields.set(inp.name, field);
      return el('label', { class: 'field' }, el('span', { class: 'dim', text: inp.why || inp.name }), field);
    }));
  }
  target.addEventListener('change', syncInputs);
  target.addEventListener('input', syncInputs);

  const env = el('select', {}, el('option', { value: 'simple' }, 'simple'), el('option', { value: 'stack' }, 'stack (database)'));
  // A project with a worktree runs IN its worktree (spawn.go), which is the
  // whole point of one worktree per feature. The box is the way to say "no,
  // this checkout" — the CLI's --here, on the surface where the person can see
  // which directory they are about to write into.
  const here = el('input', { type: 'checkbox' });

  // Making the checkout. Separate button, separate request: creating a branch is
  // cheap and reversible, starting a run spends money, and a person setting up
  // three of these before starting any must be able to do the first half alone.
  const wtName = el('input', { placeholder: 'name (73960)' });
  const wtBranch = el('input', { placeholder: 'branch (feat/<name>)' });
  const wtBase = el('input', { placeholder: 'base', value: 'main' });
  const maker = el('div', { class: 'row hidden' },
    wtName, wtBranch, wtBase,
    el('button', {
      onclick: async () => {
        try {
          const res = await api.post('/api/worktrees', {
            repo: repo.value, name: wtName.value.trim(),
            branch: wtBranch.value.trim(), base: wtBase.value.trim(),
          });
          await loadWorktrees();
          worktree.value = res.path;
          await loadRecipes();
          status(`${res.branch} at ${res.path}${res.warning ? ' — ' + res.warning : ''} — ${res.command}`, res.warning ? 'warn' : 'ok');
        } catch (e) { status(e.message, 'bad'); }
      },
    }, 'Create'),
  );

  const box = el('div', { class: 'card' },
    el('div', { class: 'row' },
      repo,
      worktree,
      el('button', { onclick: () => maker.classList.toggle('hidden') }, '+ worktree'),
    ),
    maker,
    el('div', { class: 'row' },
      target,
      recipeList,
      env,
      el('label', { class: 'check' }, here, ' run here'),
      el('button', {
        class: 'primary',
        onclick: async () => {
          const inputs = {};
          inputFields.forEach((field, name) => { if (field.value.trim()) inputs[name] = field.value.trim(); });
          try {
            const res = await api.post('/api/runs', {
              target: target.value.trim(), environment: env.value, here: here.checked,
              repo: worktree.value || repo.value, inputs,
            });
            status(`started in ${res.dir} — ${res.command}`, 'ok');
            refresh();
          } catch (e) { status(e.message, 'bad'); }
        },
      }, 'Run'),
    ),
    inputsBox,
    el('div', { class: 'dim', text: 'The run is detached: closing this page does not stop it. It runs in the checkout selected above; "+ worktree" cuts a new branch from a base and adds it to that list.' }),
  );
  const view = $('#view');
  view.prepend(box);
  // The checkout list first: the recipes are read from whichever checkout it
  // settles on.
  loadWorktrees().then(loadRecipes);
}

// ── leaving a detail screen ─────────────────────────────────────────────────
// Every way out of a detail goes through here, and every one of them re-reads.
// Not "re-reads if we noticed a change": while a detail is open this page is
// DEAF by design — the stream event is dropped so the evidence under the
// reader's eyes does not move, and the fallback poll skips its tick for the same
// reason. So there is no observer left to set a flag, and a stream that was down
// for the whole visit would never have delivered one anyway.
//
// The rule this enforces is the one the screen is for: no sequence of (open a
// detail, the state moves, come back) may leave the old screen up. An approved
// gate showing as waiting is not a stale pixel, it is the inbox asking someone
// to decide something that is already decided.
//
// The cost is one GET /api/state per back button, against a server on this
// machine. That is the whole price of making it true by construction instead of
// by bookkeeping.
function leaveDetail() {
  state.detail = null;
  return refresh();
}

// ── run detail (2d/2f) ──────────────────────────────────────────────────────
async function openRun(id) {
  state.detail = { kind: 'run', id };
  render();
}

async function renderRunDetail(root, id) {
  const r = await api.get(`/api/runs/${encodeURIComponent(id)}`);
  root.append(el('div', { class: 'row' },
    el('button', { class: 'ghost', onclick: leaveDetail }, '← back'),
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
        el('span', { class: 'id', text: p.phase, style: { width: '12ch' } }),
        el('span', { class: 'dim', text: money(p.cost_usd), style: { width: '8ch' } }),
        el('span', { class: 'bar', style: { width: `${share}%` } }),
      ));
    }
    root.append(bars);
  }

  // The step that ended the run is named at the top, not left to be found by
  // scanning a list of pills. A run reported as `failed` with nothing saying
  // WHERE is the same dead end the dispatch used to be.
  const failed = (r.tasks || []).find((t) => t.status === 'FAILED');
  if (failed) {
    root.append(el('div', { class: 'card bad' },
      el('div', { class: 'row' },
        el('span', { class: 'pill bad', text: 'failed at' }),
        el('span', { class: 'mono-id', text: failed.id }),
        el('span', { class: 'grow', text: failed.title }),
        el('button', { class: 'primary', onclick: () => openStep(r.run_id || id, failed.id) }, 'Why'),
        // Re-execute THAT step, not the run. With a fan-out over a board, one
        // story of eight failing is the ordinary case, and redoing the seven
        // that passed is both the money and the risk.
        r.liveness === 'alive' ? null
          : el('button', { onclick: () => retryStep(r.run_id || id, failed.id) }, 'Retry step'),
      ),
    ));
  }

  const steps = el('div', { class: 'steps' });
  for (const t of r.tasks || []) {
    // Every step opens, not just the failed one: "what did the step that
    // PASSED actually do" is the other half of reading a run, and until now the
    // answer was only on the CLI (`run show <id> --step S03`). A screen that
    // can show less than the terminal is a screen people leave.
    steps.append(el('div', { class: 'step clickable', onclick: () => openStep(r.run_id || id, t.id) },
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

// ── one step (2d, the half the UI did not have) ─────────────────────────────
// `corvex run show <id> --step S03` has always answered "why did this fail":
// the ledger's timeline plus the tail of what the command printed, which lives
// in machine-local scratch because command output is not committable. The API
// carried it (`?step=`) and nothing on the screen asked for it — so the one
// question an operator has at 2am was terminal-only, in a tool whose point is
// that it does not need the terminal.
async function openStep(id, step) {
  state.detail = { kind: 'step', id, step };
  render();
}

async function renderStepDetail(root, id, step) {
  const d = await api.get(`/api/runs/${encodeURIComponent(id)}?step=${encodeURIComponent(step)}`);
  const s = d.step || {};
  root.append(el('div', { class: 'row' },
    el('button', { class: 'ghost', onclick: () => openRun(id) }, '← run'),
    el('span', { class: 'mono-id', text: s.id || step }),
    el('span', { class: `pill ${s.status === 'FAILED' ? 'bad' : s.status === 'PASSED' ? 'det' : ''}`, text: s.status || 'PENDING' }),
    el('span', { class: 'grow', text: s.title || '' }),
    s.retries ? el('span', { class: 'pill warn', text: `${s.retries} retr${s.retries === 1 ? 'y' : 'ies'}` }) : null,
    s.cost_usd ? el('span', { class: 'dim', text: money(s.cost_usd) }) : null,
  ));

  if (s.status === 'FAILED') {
    root.append(el('div', { class: 'card bad' },
      el('div', { class: 'row' },
        el('span', { class: 'grow dim', text: 'Re-executar só este step: o resto do run fica como está.' }),
        el('button', { class: 'primary', onclick: () => retryStep(id, s.id || step) }, 'Retry step'),
      ),
    ));
  }
  if (s.description) root.append(el('div', { class: 'card' }, el('div', { class: 'dim', text: s.description })));
  if ((s.criteria || []).length) {
    root.append(el('div', { class: 'card' },
      el('strong', { text: 'criteria' }),
      ...s.criteria.map((c) => el('div', { class: 'dim', text: `· ${c}` })),
    ));
  }

  // The output first when there is one: it is the answer to the question that
  // brought the reader here, and the timeline is the context around it.
  if (s.output) {
    root.append(el('div', { class: 'card' },
      el('div', { class: 'row' }, el('strong', { class: 'grow', text: 'what the step printed' })),
      el('pre', { class: 'log', text: s.output }),
    ));
  }

  const events = s.events || [];
  if (!events.length) {
    root.append(el('p', { class: 'empty', text: 'The ledger has nothing for this step yet.' }));
    return;
  }
  const rows = el('div', { class: 'steps' });
  for (const ev of events) {
    rows.append(el('div', { class: 'step' },
      el('span', { class: 'id', text: new Date(ev.timestamp || ev.at).toLocaleTimeString() }),
      ev.phase ? kindPill(ev.phase === 'worker' ? 'code' : ev.phase) : null,
      el('span', { class: `pill ${String(ev.status).toLowerCase() === 'failed' ? 'bad' : ''}`, text: ev.status || ev.type || '' }),
      el('span', { class: 'grow dim', text: ev.message || ev.tool || '' }),
      ev.duration_ms ? el('span', { class: 'dim', text: human(ev.duration_ms * 1e6) }) : null,
      ev.cost_usd ? el('span', { class: 'dim', text: money(ev.cost_usd) }) : null,
    ));
  }
  root.append(el('div', { class: 'card' }, rows));
}

async function retryStep(id, step) {
  if (!confirm(`Re-executar ${step} de ${id}? O step volta a PENDING e roda de novo.`)) return;
  try {
    const res = await api.post(`/api/runs/${encodeURIComponent(id)}/retry`, { step });
    status(`re-executando ${step} — ${res.command}`, 'ok');
    leaveDetail();
  } catch (e) { status(e.message, 'bad'); }
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

async function pauseRun(id) {
  try {
    const res = await api.post(`/api/runs/${encodeURIComponent(id)}/pause`);
    status(`paused — ${res.action.command}; steps already running finish first`, 'ok');
  } catch (e) { status(e.message, 'bad'); }
  refresh();
}

async function resumeRun(id) {
  try {
    const res = await api.post(`/api/runs/${encodeURIComponent(id)}/resume`);
    status(`resumed — ${res.action.command}`, 'ok');
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
  // Seeded from what is already on disk, not from an empty set. The reading
  // state F5 built is persistent precisely so closing the tab does not undo it —
  // and the CLI (`corvex gate ack`) writes into the same place. A lock that
  // ignored it would tell someone who already read the evidence that they had
  // not, which is how a real check trains people to click past it.
  const alreadyRead = new Set((g.reads || []).map((r) => r.label));
  const read = new Set(alreadyRead);

  const approve = el('button', { class: 'primary', disabled: required.length ? '' : null }, 'Approve');
  const relock = () => { approve.toggleAttribute('disabled', read.size < required.length); };

  root.append(el('div', { class: 'row' },
    el('button', { class: 'ghost', onclick: leaveDetail }, '← back'),
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
              checked: alreadyRead.has(e.label) ? '' : null,
              onchange: (ev) => { ev.target.checked ? read.add(e.label) : read.delete(e.label); relock(); },
            }),
            el('span', { class: 'required', text: alreadyRead.has(e.label) ? 'read' : 'I read this' }))
        : null,
    );
    root.append(el('div', { class: 'evidence' }, head, body));
  }

  // 2g: the gate that asked. It gets the verb that answers it and NOT the one
  // that approves — the server refuses an approval here, and offering a button
  // that is always refused is worse than offering none. Rejecting stays, because
  // declining to answer is a real answer and already fails the step.
  if (g.nature === 'question') {
    const text = el('textarea', { rows: 3, placeholder: 'your answer' });
    const send = el('button', { class: 'primary' }, 'Answer');
    const decline = el('button', { class: 'danger' }, 'Decline');
    send.addEventListener('click', async () => {
      try {
        const res = await api.post(`/api/gates/${encodeURIComponent(g.run_id)}/answer`, { step: g.step_id, ack: [...read], text: text.value });
        status(`answered — ${res.action.command}`, 'ok');
        leaveDetail();
      } catch (e) { status(e.message, 'bad'); }
    });
    // Declining sends whatever is in the box as the REASON. Whoever declines a
    // question usually has half an answer — "I do not know either, ask the
    // release owner" — and that sentence is the most useful thing the next
    // reader could have. It also means nothing typed is ever thrown away by
    // pressing the other button.
    decline.addEventListener('click', async () => {
      try {
        const res = await api.post(`/api/gates/${encodeURIComponent(g.run_id)}/reject`, { step: g.step_id, reason: text.value });
        status(`declined — ${res.action.command}`, 'ok');
        leaveDetail();
      } catch (e) { status(e.message, 'bad'); }
    });
    root.append(el('div', { class: 'card' },
      text,
      el('div', { class: 'row' }, el('span', { class: 'grow' }), decline, send),
      el('div', { class: 'dim', text: 'The same reply from a terminal: corvex gate answer <id> --step <step> --text "…"' }),
    ));
    return;
  }

  const reason = el('input', { placeholder: 'why (optional)', class: 'grow' });
  approve.addEventListener('click', async () => {
    try {
      const res = await api.post(`/api/gates/${encodeURIComponent(g.run_id)}/approve`, { step: g.step_id, ack: [...read] });
      status(`approved — ${res.action.command}`, 'ok');
      leaveDetail();
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
            leaveDetail();
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

// ── the stream, and the poll under it ───────────────────────────────────────
// The server watches the disk and tells us when it changed (internal/server/
// stream.go). Under that it is still a poll — it just moved to the side that can
// hold one connection open instead of asking twelve times a minute.
//
// Nothing here parses state out of the event. The payload says "something
// changed"; the page then re-reads /api/state through the same authenticated
// route it always used. That keeps one shape on the wire and keeps evidence out
// of a channel that exists to carry a nudge.
const POLL_MS = 5000;
// Three missed heartbeats. The server pings every 10s while quiet, so silence
// this long means the stream is gone even if the browser has not said so yet.
const STREAM_TRUST_MS = 35000;

const streamAlive = () => state.streamAt > 0 && Date.now() - state.streamAt < STREAM_TRUST_MS;

function startStream() {
  if (typeof EventSource !== 'function') return; // no stream: the poll above is already the whole answer
  const es = new EventSource('/api/events');
  // A ping counts as proof of life exactly like a change does: that is what lets
  // the fallback poll stand down during a quiet hour without going blind.
  const alive = () => { state.streamAt = Date.now(); };
  es.addEventListener('open', alive);
  es.addEventListener('ping', alive);
  es.addEventListener('state', (ev) => {
    alive();
    // The fingerprint is why the payload carries anything at all, and it buys
    // exactly one thing: the stream states its current position on every
    // connect, so a stream that flapped and came back would otherwise trigger a
    // full re-read for nothing. It is a change detector for the CONNECTION, so
    // it is consumed the moment it is seen — never held back, because a
    // fingerprint kept for later is a fingerprint that has to be reconciled
    // later, and nothing here reconciles it.
    let fp = '';
    try { fp = JSON.parse(ev.data).fingerprint || ''; } catch (_) { fp = ''; }
    if (fp && fp === state.fingerprint) return;
    state.fingerprint = fp;
    // A detail screen is not redrawn under the reader's hands: 2b is the screen
    // someone reads BEFORE approving, and swapping evidence mid-read is how a
    // person approves something they did not read. So the event is dropped —
    // and dropping it is only safe because leaveDetail() re-reads on the way
    // out, unconditionally. See the comment there.
    if (state.detail) return;
    refresh();
  });
  // EventSource reconnects on its own, so there is nothing to retry here. What
  // matters is admitting the stream is down NOW, so the next tick of the poll
  // does the work instead of skipping it.
  es.addEventListener('error', () => { state.streamAt = 0; });
  state.stream = es;
}

// ── shell ───────────────────────────────────────────────────────────────────
async function render() {
  const root = $('#view');
  root.replaceChildren();
  try {
    if (state.detail?.kind === 'run') return await renderRunDetail(root, state.detail.id);
    if (state.detail?.kind === 'gate') return await renderGateDetail(root, state.detail.id, state.detail.step);
    if (state.detail?.kind === 'log') return await renderLogDetail(root, state.detail.name);
    if (state.detail?.kind === 'step') return await renderStepDetail(root, state.detail.id, state.detail.step);
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
    // Attention is announced from HERE and not from the inbox renderer: the
    // question "is something waiting on me" does not depend on which tab is
    // open, and the first version of this only updated the title while the
    // inbox happened to be the visible screen — which is the one moment the
    // badge is redundant.
    attentionFrom(state.data);
    await render();
  } catch (e) { status(e.message, 'bad'); }
}

// attentionFrom counts what is blocked on a person and hands it to the two
// channels outside this tab.
function attentionFrom(data) {
  const gates = data.inbox?.gates || [];
  const escalations = data.inbox?.escalations || [];
  const dead = (data.dispatches || []).filter((d) => d.result && d.result !== 'ok');
  $('#badge-inbox').textContent = gates.length + escalations.length + dead.length || '';
  announceWaiting(gates.length + escalations.length + dead.length, [
    ...gates.map((g) => `${g.gate.run_id}: ${g.gate.label || g.gate.title || 'gate'}`),
    ...escalations.map((e) => `${e.project} ${e.step}: escalation`),
    ...dead.map((d) => `dispatch: ${d.result}`),
  ]);
}

function boot() {
  // The token did its job on the first request: the server answered with a
  // cookie. Leaving it in the address bar is what makes it end up in a
  // screenshot of the gate inbox or in a pasted link — and the comment in
  // index.go claimed this already happened, which it did not until here.
  if (location.search.includes('token=')) {
    history.replaceState(null, '', location.pathname);
  }

  for (const tab of document.querySelectorAll('.tab')) {
    tab.addEventListener('click', () => {
      state.view = tab.dataset.view;
      for (const t of document.querySelectorAll('.tab')) t.classList.toggle('active', t === tab);
      // A tab is the other way out of a detail, so it takes the same exit. It
      // re-reads even when no detail was open: the tab bar is where someone goes
      // to ask "what is there now", and answering that from a cached payload is
      // the same lie in a cheaper wrapper.
      leaveDetail();
    });
  }
  document.querySelector('.tab').classList.add('active');
  // Permission is asked on a gesture, never on load — see announceWaiting. The
  // button also states the current answer, because "did I turn that on" is
  // otherwise a thing people test by waiting for a gate.
  const notify = $('#notify-toggle');
  const paintNotify = () => {
    if (typeof Notification !== 'function') { notify.textContent = 'No notify'; notify.disabled = true; return; }
    notify.textContent = Notification.permission === 'granted' ? 'Notify: on'
      : Notification.permission === 'denied' ? 'Notify: blocked' : 'Notify';
  };
  notify.addEventListener('click', async () => {
    if (typeof Notification !== 'function') return;
    try { await Notification.requestPermission(); } catch (_) { /* older API shape */ }
    paintNotify();
  });
  paintNotify();
  $('#palette-open').addEventListener('click', openPalette);
  $('#palette-close').addEventListener('click', () => $('#palette').classList.add('hidden'));
  document.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); openPalette(); }
    if (e.key === 'Escape') $('#palette').classList.add('hidden');
  });
  refresh();
  startStream();
  // The poll never goes away, it stands down. While the stream is proving itself
  // alive this timer does nothing; the moment it stops proving it, the page is
  // back to exactly the five-second poll it shipped with. A page that trusted
  // the stream and went mute when the stream died would be strictly worse than
  // polling — the screen would look current and be wrong, and the whole reason
  // this screen exists is that something is blocked on a person.
  state.timer = setInterval(() => {
    if (state.detail) return;
    if (streamAlive()) return;
    refresh();
  }, POLL_MS);
}

boot();
