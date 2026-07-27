(function () {
  const searchInput = document.getElementById('search');
  const tooltip = document.getElementById('tooltip');
  const panel = document.getElementById('panel');
  const panelContent = document.getElementById('panel-content');
  const copyBtn = document.getElementById('copy-md');
  const copyVisibleBtn = document.getElementById('copy-visible-paths');
  const syncBtn = document.getElementById('sync-btn');

  let cy = null;
  let allEdges = [];
  let currentMarkdown = '';

  function projectColor(project) {
    if (!project) return '#7fb069';
    let h = 0;
    for (let i = 0; i < project.length; i++) h = (h * 31 + project.charCodeAt(i)) % 360;
    return `hsl(${h}, 65%, 48%)`;
  }

  function seedLayout(graph) {
    const groups = {};
    for (const n of graph.nodes) {
      const p = n.project || 'general';
      if (!groups[p]) groups[p] = { project: p, hub: null, members: [] };
      if (n.type === 'namespace') groups[p].hub = n;
      else groups[p].members.push(n);
    }

    const golden = 2.39996322972865332;
    const spacing = 28;
    const list = Object.values(groups).map(g => {
      g.members.sort((a, b) => a.name.localeCompare(b.name));
      const outerR = 36 + spacing * Math.sqrt(Math.max(1, g.members.length));
      g.bounding = outerR + 38;
      return g;
    }).sort((a, b) => b.bounding - a.bounding);

    const pos = {};
    const offsets = {};
    const cx = 0, cy = 0;
    const placed = [];

    for (let i = 0; i < list.length; i++) {
      const g = list[i];
      let gx = cx, gy = cy;
      if (i > 0) {
        let minSep = 0;
        for (const p of placed) minSep = Math.max(minSep, p.bounding + g.bounding + 28);
        let angle = i * golden, dist = minSep;
        for (let attempt = 0; attempt < 8000; attempt++) {
          gx = cx + Math.cos(angle) * dist;
          gy = cy + Math.sin(angle) * dist;
          let overlap = false;
          for (const p of placed) {
            const d = Math.hypot(gx - p.cx, gy - p.cy);
            if (d < p.bounding + g.bounding) { overlap = true; break; }
          }
          if (!overlap) break;
          angle += 0.12;
          dist += 2.2;
        }
      }
      g.cx = gx; g.cy = gy;
      placed.push(g);

      if (g.hub) pos[g.hub.id] = { x: gx, y: gy };

      for (let k = 0; k < g.members.length; k++) {
        const r = 34 + spacing * Math.sqrt(k);
        const a = k * golden;
        const m = g.members[k];
        pos[m.id] = { x: gx + Math.cos(a) * r, y: gy + Math.sin(a) * r };
        offsets[m.id] = { lmx: Math.cos(a) * 14, lmy: Math.sin(a) * 14 };
      }
    }
    return { pos, offsets };
  }

  function escapeHtml(s) {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }

  function renderMarkdown(md) {
    const parts = md.split(/```/);
    let html = '';
    for (let i = 0; i < parts.length; i++) {
      if (i % 2 === 1) html += `<pre><code>${escapeHtml(parts[i])}</code></pre>`;
      else html += `<p>${escapeHtml(parts[i]).replace(/\n/g, '<br>')}</p>`;
    }
    return html;
  }

  function showPanel(id) {
    panel.classList.remove('hidden');
    panelContent.innerHTML = '<p>Loading…</p>';
    currentMarkdown = '';
    fetch(`/api/nodes/${encodeURIComponent(id)}`)
      .then(r => { if (!r.ok) throw new Error(r.statusText); return r.text(); })
      .then(md => { currentMarkdown = md; panelContent.innerHTML = renderMarkdown(md); })
      .catch(err => { panelContent.innerHTML = `<p style="color:#c77">Error: ${escapeHtml(err.message)}</p>`; });
  }

  copyBtn.addEventListener('click', () => {
    if (currentMarkdown) {
      navigator.clipboard.writeText(currentMarkdown).catch(() => {});
      const original = copyBtn.textContent;
      copyBtn.textContent = 'Copied!';
      setTimeout(() => (copyBtn.textContent = original), 1200);
    }
  });

  closeBtn.addEventListener('click', () => { panel.classList.add('hidden'); });

  function runLayout(graph) {
    const { pos, offsets } = seedLayout(graph);
    return graph.nodes.map(n => ({
      id: n.id,
      x: (pos[n.id] || { x: 0 }).x,
      y: (pos[n.id] || { y: 0 }).y,
      lmx: (offsets[n.id] || { lmx: 0 }).lmx,
      lmy: (offsets[n.id] || { lmy: 0 }).lmy
    }));
  }

  function buildGraph(graph) {
    allEdges = graph.edges || [];
    const layoutNodes = runLayout(graph);
    const positions = {};
    const offsets = {};
    for (const n of layoutNodes) {
      positions[n.id] = { x: n.x, y: n.y };
      offsets[n.id] = { lmx: n.lmx, lmy: n.lmy };
    }

    const degrees = {};
    for (const n of graph.nodes) degrees[n.id] = 0;
    for (const e of allEdges) {
      degrees[e.source] = (degrees[e.source] || 0) + 1;
      degrees[e.target] = (degrees[e.target] || 0) + 1;
    }

    const elements = [];
    for (const n of graph.nodes) {
      const isNs = n.type === 'namespace';
      const p = positions[n.id] || { x: 0, y: 0 };
      const off = offsets[n.id] || { lmx: 0, lmy: 0 };
      elements.push({
        group: 'nodes',
        data: { id: n.id, name: n.name, type: n.type, project: n.project, tags: n.tags, file_path: n.file_path, degree: degrees[n.id] || 0, lmx: off.lmx, lmy: off.lmy, color: isNs ? '#8b5a2b' : projectColor(n.project) },
        position: p,
        classes: isNs ? 'namespace' : 'skill'
      });
    }

    for (const e of allEdges) {
      if (e.relation === 'shared-keyword') continue;
      elements.push({
        group: 'edges',
        data: { source: e.source, target: e.target, relation: e.relation },
        classes: e.relation + '-edge' + (e.relation === 'similar' ? ' hidden' : '')
      });
    }

    const style = [
      { selector: 'node', style: { 'font-family': 'system-ui, sans-serif', 'text-outline-color': '#0f1a12', 'text-outline-width': 1, 'min-zoomed-font-size': 6 } },
      { selector: '.namespace', style: { 'background-color': '#8b5a2b', 'border-color': '#8b5a2b', 'border-width': 1, 'border-opacity': 0.7, 'shape': 'ellipse', 'width': 30, 'height': 20, 'label': 'data(name)', 'text-valign': 'top', 'text-halign': 'center', 'color': '#e8f0e8', 'font-size': 10, 'min-zoomed-font-size': 5 } },
      { selector: '.namespace:selected', style: { 'border-opacity': 1, 'background-blacken': 0.1 } },
      { selector: '.skill', style: { 'background-color': 'data(color)', 'border-color': '#0f1a12', 'border-width': 1, 'width': 8, 'height': 8, 'shape': 'ellipse', 'label': 'data(name)', 'text-valign': 'center', 'text-halign': 'center', 'text-margin-x': 'data(lmx)', 'text-margin-y': 'data(lmy)', 'color': '#e8f0e8', 'font-size': 'mapData(degree, 0, 30, 7, 12)', 'min-zoomed-font-size': 0, 'text-max-width': 80, 'text-overflow': 'ellipsis', 'text-background-color': '#0f1a12', 'text-background-opacity': 0.75, 'text-background-padding': 1 } },
      { selector: '.skill:selected', style: { 'font-size': 9, 'text-outline-width': 2, 'z-index': 999 } },
      { selector: 'edge', style: { 'curve-style': 'haystack', 'width': 1, 'target-arrow-shape': 'none' } },
      { selector: '.namespace-edge', style: { 'line-color': '#8b5a2b', 'opacity': 0.35, 'width': 1 } },
      { selector: '.references-edge', style: { 'line-color': '#7fb069', 'opacity': 0.12, 'width': 0.5 } },
      { selector: '.shared-keyword-edge', style: { 'line-color': '#a8c68a', 'opacity': 0.08, 'width': 0.6 } },
      { selector: '.similar-edge', style: { 'line-color': '#6d9a5e', 'opacity': 0.35, 'width': 1 } },
      { selector: '.hidden', style: { 'display': 'none' } }
    ];

    cy = cytoscape({
      container: document.getElementById('cy'),
      elements,
      style,
      layout: { name: 'preset' },
      minZoom: 0.05,
      maxZoom: 5,
      wheelSensitivity: 0.25,
      boxSelectionEnabled: false
    });

    cy.panningEnabled(true);
    cy.zoomingEnabled(true);
    cy.userPanningEnabled(true);
    cy.userZoomingEnabled(true);

    cy.on('tap', 'node', evt => { showPanel(evt.target.id()); });
    cy.on('tap', evt => { if (evt.target === cy) panel.classList.add('hidden'); });

    cy.on('mouseover', 'node', evt => {
      const n = evt.target;
      tooltip.classList.remove('hidden');
      tooltip.textContent = n.data('name') || n.id();
      tooltip.style.left = (evt.originalEvent.clientX + 12) + 'px';
      tooltip.style.top = (evt.originalEvent.clientY + 12) + 'px';
    });
    cy.on('mouseout', 'node', () => tooltip.classList.add('hidden'));

    cy.fit(40);
  }

  function toggleEdge(type, show) {
    if (!cy) return;
    if (type === 'shared-keyword') {
      if (show) {
        const existing = new Set(cy.edges('.shared-keyword-edge').map(e => e.source().id() + '-' + e.target().id()));
        const newEdges = allEdges
          .filter(e => e.relation === 'shared-keyword' && !existing.has(`${e.source}-${e.target}`))
          .map(e => ({ group: 'edges', data: { source: e.source, target: e.target, relation: e.relation }, classes: 'shared-keyword-edge' }));
        if (newEdges.length) cy.add(newEdges);
      } else {
        cy.edges('.shared-keyword-edge').remove();
      }
      return;
    }
    cy.batch(() => {
      for (const e of cy.edges(`.${type}-edge`)) {
        if (show) e.removeClass('hidden');
        else e.addClass('hidden');
      }
    });
  }

  function updateFilter() {
    if (!cy) return;
    const q = searchInput.value.trim().toLowerCase();
    cy.batch(() => {
      for (const n of cy.nodes()) {
        if (!q) { n.removeClass('hidden'); continue; }
        const d = n.data();
        const inName = (d.name || '').toLowerCase().includes(q);
        const inProject = (d.project || '').toLowerCase().includes(q);
        const inTags = (d.tags || []).some(t => t.toLowerCase().includes(q));
        if (inName || inProject || inTags) n.removeClass('hidden');
        else n.addClass('hidden');
      }
    });
  }

  document.querySelectorAll('.edge-toggle').forEach(cb => {
    cb.addEventListener('change', () => toggleEdge(cb.value, cb.checked));
  });
  searchInput.addEventListener('input', updateFilter);
  searchInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && cy) {
      const q = searchInput.value.trim().toLowerCase();
      const targets = q ? cy.nodes(':visible') : cy.nodes();
      if (targets.length) cy.fit(targets, 60);
    }
  });

  if (copyVisibleBtn) {
    copyVisibleBtn.addEventListener('click', () => {
      if (!cy) return;
      const nodes = cy.nodes(':visible').filter(n => n.data('type') !== 'namespace');
      const paths = nodes.map(n => n.data('file_path') || n.data('id')).filter(Boolean);
      if (paths.length === 0) return;
      const text = paths.join('\n');
      navigator.clipboard.writeText(text).then(() => {
        const orig = copyVisibleBtn.textContent;
        copyVisibleBtn.textContent = `Copied ${paths.length} path${paths.length === 1 ? '' : 's'}!`;
        setTimeout(() => { copyVisibleBtn.textContent = orig; }, 1500);
      }).catch(err => {
        console.error('Clipboard copy failed:', err);
      });
    });
  }

  if (syncBtn) {
    syncBtn.addEventListener('click', async () => {
      syncBtn.disabled = true;
      const orig = syncBtn.textContent;
      syncBtn.textContent = 'Syncing…';
      try {
        const res = await fetch('/api/sync', { method: 'POST' });
        if (!res.ok) throw new Error(await res.text());
        await init();
        syncBtn.textContent = 'Synced!';
      } catch (err) {
        syncBtn.textContent = 'Failed';
        console.error('Sync failed:', err);
      } finally {
        setTimeout(() => {
          syncBtn.disabled = false;
          syncBtn.textContent = orig;
        }, 1500);
      }
    });
  }

  window.addEventListener('resize', () => { if (cy) { cy.resize(); cy.fit(40); } });

  async function init() {
    try {
      const res = await fetch('/api/graph');
      if (!res.ok) throw new Error(res.statusText);
      const graph = await res.json();
      buildGraph(graph);
    } catch (err) {
      console.error(err);
      document.getElementById('cy').textContent = 'Failed to load graph: ' + err.message;
    }
  }

  init();
})();
