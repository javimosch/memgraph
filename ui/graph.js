import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';

(function () {
  const searchInput = document.getElementById('search');
  const tooltip = document.getElementById('tooltip');
  const panel = document.getElementById('panel');
  const panelContent = document.getElementById('panel-content');
  const copyBtn = document.getElementById('copy-md');
  const closeBtn = document.getElementById('panel-close');
  const copyVisibleBtn = document.getElementById('copy-visible-paths');
  const syncBtn = document.getElementById('sync-btn');

  let scene, camera, renderer, controls;
  let nodeMeshes = [];
  let glowPoints = null;
  let glowData = [];
  let nodeLabels = [];
  let edgeLines = [];
  let allEdges = [];
  let nodeMap = {};
  let raycaster, mouse;
  let hoveredNode = null;
  let currentMarkdown = '';
  let starField = null;
  let dustField = null;
  let glowTexture = null;

  function makeGlowTexture() {
    if (glowTexture) return glowTexture;
    const size = 64;
    const canvas = document.createElement('canvas');
    canvas.width = size;
    canvas.height = size;
    const ctx = canvas.getContext('2d');
    const grad = ctx.createRadialGradient(size/2, size/2, 0, size/2, size/2, size/2);
    grad.addColorStop(0, 'rgba(255,255,255,1)');
    grad.addColorStop(0.2, 'rgba(255,255,255,0.6)');
    grad.addColorStop(0.5, 'rgba(255,255,255,0.15)');
    grad.addColorStop(1, 'rgba(255,255,255,0)');
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, size, size);
    glowTexture = new THREE.CanvasTexture(canvas);
    glowTexture.minFilter = THREE.LinearFilter;
    glowTexture.magFilter = THREE.LinearFilter;
    return glowTexture;
  }

  function projectColor(project) {
    if (!project) return { r: 0.5, g: 0.69, b: 0.41 };
    let h = 0;
    for (let i = 0; i < project.length; i++) h = (h * 31 + project.charCodeAt(i)) % 360;
    const c = new THREE.Color();
    c.setHSL(h / 360, 0.7, 0.55);
    return { r: c.r, g: c.g, b: c.b };
  }

  const labelTextureCache = {};
  function makeLabelTexture(text, fontSize, color) {
    const key = `${fontSize}:${color}:${text}`;
    if (labelTextureCache[key]) return labelTextureCache[key];

    const canvas = document.createElement('canvas');
    const ctx = canvas.getContext('2d');
    ctx.font = `${fontSize}px system-ui, sans-serif`;
    const metrics = ctx.measureText(text);
    const w = Math.ceil(metrics.width) + 8;
    const h = fontSize + 6;
    canvas.width = w;
    canvas.height = h;
    ctx.font = `${fontSize}px system-ui, sans-serif`;
    ctx.fillStyle = 'rgba(0, 0, 0, 0)';
    ctx.fillRect(0, 0, w, h);
    ctx.shadowColor = 'rgba(100, 180, 255, 0.9)';
    ctx.shadowBlur = 4;
    ctx.fillStyle = color;
    ctx.textBaseline = 'middle';
    ctx.fillText(text, 4, h / 2);

    const tex = new THREE.CanvasTexture(canvas);
    tex.minFilter = THREE.LinearFilter;
    tex.magFilter = THREE.LinearFilter;
    labelTextureCache[key] = tex;
    return tex;
  }

  function makeLabel(text, fontSize, colorHex) {
    const tex = makeLabelTexture(text, fontSize, colorHex);
    const mat = new THREE.SpriteMaterial({ map: tex, transparent: true, depthTest: false, blending: THREE.AdditiveBlending });
    const sprite = new THREE.Sprite(mat);
    sprite.scale.set(tex.image.width * 0.12, tex.image.height * 0.12, 1);
    sprite.renderOrder = 999;
    return sprite;
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
      const orig = copyBtn.textContent;
      copyBtn.textContent = 'Copied!';
      setTimeout(() => copyBtn.textContent = orig, 1200);
    }
  });

  closeBtn.addEventListener('click', () => panel.classList.add('hidden'));

  // Galaxy-style layout: spiral arms with project clusters as star systems
  function galaxyLayout(graph) {
    const groups = {};
    for (const n of graph.nodes) {
      const p = n.project || 'general';
      if (!groups[p]) groups[p] = { project: p, hub: null, members: [] };
      if (n.type === 'namespace') groups[p].hub = n;
      else groups[p].members.push(n);
    }

    const list = Object.values(groups).map(g => {
      g.members.sort((a, b) => a.name.localeCompare(b.name));
      g.count = g.members.length + (g.hub ? 1 : 0);
      return g;
    }).sort((a, b) => b.count - a.count);

    const pos = {};
    const numArms = 4;
    const armSpacing = (Math.PI * 2) / numArms;
    const galaxyRadius = 40 + Math.sqrt(list.length) * 35;
    const coreRadius = 60;

    for (let i = 0; i < list.length; i++) {
      const g = list[i];
      const arm = i % numArms;
      const t = (i + 1) / list.length;
      const distFromCore = coreRadius + t * galaxyRadius;
      const spiralTightness = 2.2;
      const baseAngle = arm * armSpacing + t * spiralTightness;
      const jitter = (Math.sin(i * 7.13) * 0.15);
      const angle = baseAngle + jitter;
      const cx = Math.cos(angle) * distFromCore;
      const cy = Math.sin(angle) * distFromCore;

      if (g.hub) pos[g.hub.id] = { x: cx, y: cy };

      const golden = 2.39996322972865332;
      const spacing = 14;
      for (let k = 0; k < g.members.length; k++) {
        const r = 10 + spacing * Math.sqrt(k);
        const a = k * golden + i * 0.3;
        const m = g.members[k];
        pos[m.id] = {
          x: cx + Math.cos(a) * r,
          y: cy + Math.sin(a) * r
        };
      }
    }
    return pos;
  }

  function buildGalaxy(graph) {
    allEdges = graph.edges || [];
    const pos = galaxyLayout(graph);

    const degrees = {};
    for (const n of graph.nodes) degrees[n.id] = 0;
    for (const e of allEdges) {
      degrees[e.source] = (degrees[e.source] || 0) + 1;
      degrees[e.target] = (degrees[e.target] || 0) + 1;
    }

    nodeMeshes = [];
    glowData = [];
    nodeLabels = [];
    nodeMap = {};

    const glowPositions = [];
    const glowColors = [];
    const glowSizes = [];

    for (const n of graph.nodes) {
      const p = pos[n.id] || { x: 0, y: 0 };
      const isNs = n.type === 'namespace';
      const col = isNs ? { r: 1.0, g: 0.85, b: 0.4 } : projectColor(n.project);
      const color = new THREE.Color(col.r, col.g, col.b);
      const size = isNs ? 3.5 : 1.2 + Math.min(degrees[n.id] || 0, 20) * 0.12;

      // Core star
      const geo = new THREE.SphereGeometry(size, isNs ? 12 : 6, isNs ? 12 : 6);
      const mat = new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 1.0 });
      const mesh = new THREE.Mesh(geo, mat);
      mesh.position.set(p.x, p.y, 0);
      mesh.userData = {
        id: n.id, name: n.name, type: n.type, project: n.project,
        tags: n.tags, file_path: n.file_path, degree: degrees[n.id] || 0,
        color, size, visible: true, baseSize: size
      };
      scene.add(mesh);
      nodeMeshes.push(mesh);
      nodeMap[n.id] = mesh;

      // Glow point (batched into single Points cloud)
      const glowSize = isNs ? 18 : 10;
      glowPositions.push(p.x, p.y, 0);
      glowColors.push(col.r, col.g, col.b);
      glowSizes.push(glowSize);
      glowData.push({ nodeId: n.id, visible: true, baseOpacity: isNs ? 0.4 : 0.25 });

      // Label
      const labelText = n.name || n.id;
      const labelFontSize = isNs ? 44 : 28;
      const labelColor = isNs ? '#ffe9a8' : '#a8c8ff';
      const label = makeLabel(labelText, labelFontSize, labelColor);
      label.position.set(p.x, p.y + size + 4, 1);
      label.userData = { nodeId: n.id, visible: true };
      scene.add(label);
      nodeLabels.push(label);
    }

    // Single Points cloud for all glows (1 draw call instead of N)
    const glowGeo = new THREE.BufferGeometry();
    glowGeo.setAttribute('position', new THREE.Float32BufferAttribute(glowPositions, 3));
    glowGeo.setAttribute('color', new THREE.Float32BufferAttribute(glowColors, 3));
    const glowMat = new THREE.PointsMaterial({
      size: 20, map: makeGlowTexture(), vertexColors: true,
      transparent: true, opacity: 0.35, sizeAttenuation: true,
      blending: THREE.AdditiveBlending, depthWrite: false
    });
    glowPoints = new THREE.Points(glowGeo, glowMat);
    scene.add(glowPoints);

    // Edges as faint nebula filaments
    const edgePositions = [];
    const edgeColors = [];
    const edgeMeta = [];
    for (const e of allEdges) {
      if (e.relation === 'shared-keyword') continue;
      const s = pos[e.source], t = pos[e.target];
      if (!s || !t) continue;
      edgePositions.push(s.x, s.y, 0, t.x, t.y, 0);
      let col;
      if (e.relation === 'namespace') col = [0.6, 0.5, 0.3];
      else if (e.relation === 'references') col = [0.3, 0.5, 0.7];
      else if (e.relation === 'similar') col = [0.4, 0.6, 0.5];
      else col = [0.5, 0.6, 0.8];
      edgeColors.push(col[0], col[1], col[2], col[0], col[1], col[2]);
      edgeMeta.push({ source: e.source, target: e.target, relation: e.relation, visible: e.relation !== 'similar' });
    }

    const edgeGeo = new THREE.BufferGeometry();
    edgeGeo.setAttribute('position', new THREE.Float32BufferAttribute(edgePositions, 3));
    edgeGeo.setAttribute('color', new THREE.Float32BufferAttribute(edgeColors, 3));
    const edgeMat = new THREE.LineBasicMaterial({ vertexColors: true, transparent: true, opacity: 0.25, blending: THREE.AdditiveBlending });
    const edgeLine = new THREE.LineSegments(edgeGeo, edgeMat);
    scene.add(edgeLine);
    edgeLines.push({ line: edgeLine, meta: edgeMeta, geo: edgeGeo });

    fitCamera();
  }

  function fitCamera(targets) {
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
    const nodes = targets || nodeMeshes;
    for (const m of nodes) {
      if (!m.userData.visible) continue;
      const p = m.position;
      if (p.x < minX) minX = p.x; if (p.x > maxX) maxX = p.x;
      if (p.y < minY) minY = p.y; if (p.y > maxY) maxY = p.y;
    }
    if (minX === Infinity) { minX = -100; maxX = 100; minY = -100; maxY = 100; }
    const cx = (minX + maxX) / 2, cy = (minY + maxY) / 2;
    const dx = maxX - minX, dy = maxY - minY;
    const dist = Math.max(dx, dy) * 0.6 + 80;
    controls.target.set(cx, cy, 0);
    camera.position.set(cx, cy, dist);
    controls.update();
  }

  function toggleEdge(type, show) {
    for (const el of edgeLines) {
      for (let i = 0; i < el.meta.length; i++) {
        if (el.meta[i].relation === type) el.meta[i].visible = show;
      }
    }
    rebuildEdgeGeometry();
  }

  function rebuildEdgeGeometry() {
    const positions = [];
    const colors = [];
    for (const el of edgeLines) {
      for (const m of el.meta) {
        if (!m.visible) continue;
        const s = nodeMap[m.source], t = nodeMap[m.target];
        if (!s || !t) continue;
        if (!s.userData.visible || !t.userData.visible) continue;
        positions.push(s.position.x, s.position.y, 0, t.position.x, t.position.y, 0);
        let col;
        if (m.relation === 'namespace') col = [0.6, 0.5, 0.3];
        else if (m.relation === 'references') col = [0.3, 0.5, 0.7];
        else if (m.relation === 'similar') col = [0.4, 0.6, 0.5];
        else col = [0.5, 0.6, 0.8];
        colors.push(col[0], col[1], col[2], col[0], col[1], col[2]);
      }
      el.geo.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
      el.geo.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));
      el.geo.attributes.position.needsUpdate = true;
      el.geo.attributes.color.needsUpdate = true;
    }
  }

  function updateFilter() {
    const q = searchInput.value.trim().toLowerCase();
    const visibleIds = new Set();
    for (const m of nodeMeshes) {
      const d = m.userData;
      if (!q) { d.visible = true; m.visible = true; visibleIds.add(d.id); continue; }
      const inName = (d.name || '').toLowerCase().includes(q);
      const inProject = (d.project || '').toLowerCase().includes(q);
      const inTags = (d.tags || []).some(t => t.toLowerCase().includes(q));
      d.visible = inName || inProject || inTags;
      m.visible = d.visible;
      if (d.visible) visibleIds.add(d.id);
    }
    for (const lbl of nodeLabels) lbl.visible = visibleIds.has(lbl.userData.nodeId);
    for (const gd of glowData) gd.visible = visibleIds.has(gd.nodeId);
    rebuildEdgeGeometry();
  }

  function onSearchEnter() {
    const q = searchInput.value.trim().toLowerCase();
    if (q) {
      const targets = nodeMeshes.filter(m => m.userData.visible);
      if (targets.length) fitCamera(targets);
    } else {
      fitCamera();
    }
  }

  let draggedNode = null;
  let dragMoved = false;
  let dragStartPos = null;
  const dragPlane = new THREE.Plane(new THREE.Vector3(0, 0, 1), 0);
  const dragIntersect = new THREE.Vector3();

  function screenToWorld(event) {
    const rect = renderer.domElement.getBoundingClientRect();
    const ndc = new THREE.Vector2(
      ((event.clientX - rect.left) / rect.width) * 2 - 1,
      -((event.clientY - rect.top) / rect.height) * 2 + 1
    );
    raycaster.setFromCamera(ndc, camera);
    raycaster.ray.intersectPlane(dragPlane, dragIntersect);
    return dragIntersect.clone();
  }

  function onMouseDown(event) {
    if (event.button !== 0) return; // left only
    const rect = renderer.domElement.getBoundingClientRect();
    mouse.x = ((event.clientX - rect.left) / rect.width) * 2 - 1;
    mouse.y = -((event.clientY - rect.top) / rect.height) * 2 + 1;
    raycaster.setFromCamera(mouse, camera);
    const visible = nodeMeshes.filter(m => m.visible);
    const intersects = raycaster.intersectObjects(visible);
    if (intersects.length > 0) {
      draggedNode = intersects[0].object;
      dragMoved = false;
      dragStartPos = { x: event.clientX, y: event.clientY };
      controls.enabled = false; // disable pan while dragging a node
      renderer.domElement.style.cursor = 'grabbing';
    }
  }

  function onMouseMove(event) {
    if (draggedNode) {
      const dx = event.clientX - dragStartPos.x;
      const dy = event.clientY - dragStartPos.y;
      if (!dragMoved && Math.hypot(dx, dy) > 4) dragMoved = true;
      if (dragMoved) {
        const world = screenToWorld(event);
        draggedNode.position.set(world.x, world.y, 0);
        // Move the matching glow point
        const idx = nodeMeshes.indexOf(draggedNode);
        if (glowPoints && idx >= 0) {
          const arr = glowPoints.geometry.attributes.position.array;
          arr[idx * 3] = world.x;
          arr[idx * 3 + 1] = world.y;
          glowPoints.geometry.attributes.position.needsUpdate = true;
        }
        // Move the label
        const lbl = nodeLabels.find(l => l.userData.nodeId === draggedNode.userData.id);
        if (lbl) lbl.position.set(world.x, world.y + draggedNode.userData.size + 4, 1);
        // Update edges
        rebuildEdgeGeometry();
      }
      return;
    }

    const rect = renderer.domElement.getBoundingClientRect();
    mouse.x = ((event.clientX - rect.left) / rect.width) * 2 - 1;
    mouse.y = -((event.clientY - rect.top) / rect.height) * 2 + 1;
    raycaster.setFromCamera(mouse, camera);
    const visible = nodeMeshes.filter(m => m.visible);
    const intersects = raycaster.intersectObjects(visible);
    if (intersects.length > 0) {
      const node = intersects[0].object;
      if (hoveredNode !== node) {
        if (hoveredNode) hoveredNode.material.opacity = 1.0;
        hoveredNode = node;
        hoveredNode.material.opacity = 1.5;
        tooltip.classList.remove('hidden');
        tooltip.textContent = node.userData.name || node.userData.id;
      }
      tooltip.style.left = (event.clientX + 12) + 'px';
      tooltip.style.top = (event.clientY + 12) + 'px';
      renderer.domElement.style.cursor = 'pointer';
    } else {
      if (hoveredNode) {
        hoveredNode.material.opacity = 1.0;
        hoveredNode = null;
      }
      tooltip.classList.add('hidden');
      renderer.domElement.style.cursor = 'grab';
    }
  }

  function onMouseUp(event) {
    if (draggedNode) {
      if (!dragMoved) {
        // Treat as click — open panel
        showPanel(draggedNode.userData.id);
      }
      draggedNode = null;
      dragMoved = false;
      controls.enabled = true;
      renderer.domElement.style.cursor = 'grab';
    }
  }

  document.querySelectorAll('.edge-toggle').forEach(cb => {
    cb.addEventListener('change', () => toggleEdge(cb.value, cb.checked));
  });
  searchInput.addEventListener('input', updateFilter);
  searchInput.addEventListener('keydown', e => { if (e.key === 'Enter') onSearchEnter(); });

  if (copyVisibleBtn) {
    copyVisibleBtn.addEventListener('click', () => {
      const paths = nodeMeshes
        .filter(m => m.visible && m.userData.type !== 'namespace')
        .map(m => m.userData.file_path || m.userData.id)
        .filter(Boolean);
      if (!paths.length) return;
      navigator.clipboard.writeText(paths.join('\n')).then(() => {
        const orig = copyVisibleBtn.textContent;
        copyVisibleBtn.textContent = `Copied ${paths.length} path${paths.length === 1 ? '' : 's'}!`;
        setTimeout(() => copyVisibleBtn.textContent = orig, 1500);
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
        clearScene();
        await init();
        syncBtn.textContent = 'Synced!';
      } catch (err) {
        syncBtn.textContent = 'Failed';
        console.error('Sync failed:', err);
      } finally {
        setTimeout(() => { syncBtn.disabled = false; syncBtn.textContent = orig; }, 1500);
      }
    });
  }

  function clearScene() {
    for (const m of nodeMeshes) { scene.remove(m); m.geometry.dispose(); m.material.dispose(); }
    for (const lbl of nodeLabels) { scene.remove(lbl); if (lbl.material) lbl.material.dispose(); }
    for (const el of edgeLines) { scene.remove(el.line); el.geo.dispose(); }
    if (glowPoints) { scene.remove(glowPoints); glowPoints.geometry.dispose(); glowPoints.material.dispose(); glowPoints = null; }
    nodeMeshes = []; glowData = []; nodeLabels = []; edgeLines = []; nodeMap = {};
  }

  function createStarField() {
    const count = 1500;
    const geo = new THREE.BufferGeometry();
    const positions = new Float32Array(count * 3);
    const colors = new Float32Array(count * 3);
    for (let i = 0; i < count; i++) {
      const r = 200 + Math.random() * 1800;
      const a = Math.random() * Math.PI * 2;
      positions[i * 3] = Math.cos(a) * r;
      positions[i * 3 + 1] = Math.sin(a) * r;
      positions[i * 3 + 2] = (Math.random() - 0.5) * 50;
      const intensity = 0.3 + Math.random() * 0.7;
      const tint = Math.random();
      if (tint < 0.6) { colors[i*3] = intensity; colors[i*3+1] = intensity; colors[i*3+2] = intensity; }
      else if (tint < 0.85) { colors[i*3] = intensity * 0.7; colors[i*3+1] = intensity * 0.85; colors[i*3+2] = intensity; }
      else { colors[i*3] = intensity; colors[i*3+1] = intensity * 0.8; colors[i*3+2] = intensity * 0.5; }
    }
    geo.setAttribute('position', new THREE.BufferAttribute(positions, 3));
    geo.setAttribute('color', new THREE.BufferAttribute(colors, 3));
    const mat = new THREE.PointsMaterial({ size: 1.2, vertexColors: true, transparent: true, opacity: 0.6, sizeAttenuation: true, blending: THREE.AdditiveBlending });
    starField = new THREE.Points(geo, mat);
    scene.add(starField);
  }

  function createDustField() {
    const count = 400;
    const geo = new THREE.BufferGeometry();
    const positions = new Float32Array(count * 3);
    const colors = new Float32Array(count * 3);
    for (let i = 0; i < count; i++) {
      const r = 30 + Math.random() * 400;
      const a = Math.random() * Math.PI * 2;
      positions[i * 3] = Math.cos(a) * r;
      positions[i * 3 + 1] = Math.sin(a) * r;
      positions[i * 3 + 2] = (Math.random() - 0.5) * 10;
      const intensity = 0.05 + Math.random() * 0.1;
      colors[i*3] = intensity * 0.6;
      colors[i*3+1] = intensity * 0.7;
      colors[i*3+2] = intensity;
    }
    geo.setAttribute('position', new THREE.BufferAttribute(positions, 3));
    geo.setAttribute('color', new THREE.BufferAttribute(colors, 3));
    const mat = new THREE.PointsMaterial({ size: 8, vertexColors: true, transparent: true, opacity: 0.4, sizeAttenuation: true, blending: THREE.AdditiveBlending, depthWrite: false });
    dustField = new THREE.Points(geo, mat);
    scene.add(dustField);
  }

  function initThree() {
    const container = document.getElementById('cy');
    scene = new THREE.Scene();
    scene.background = new THREE.Color(0x000005);

    camera = new THREE.PerspectiveCamera(50, container.clientWidth / container.clientHeight, 0.1, 5000);
    camera.position.set(0, 0, 400);

    try {
      renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false });
    } catch (e) {
      container.innerHTML = '<div style="color:#aaccff;padding:2rem;font-family:system-ui">WebGL not available. <a href="/" style="color:#7fb069">Go to 2D version</a></div>';
      return false;
    }
    renderer.setSize(container.clientWidth, container.clientHeight);
    renderer.setPixelRatio(window.devicePixelRatio);
    container.appendChild(renderer.domElement);

    controls = new OrbitControls(camera, renderer.domElement);
    controls.enableDamping = true;
    controls.dampingFactor = 0.08;
    controls.enableRotate = false;
    controls.enablePan = true;
    controls.mouseButtons = { LEFT: THREE.MOUSE.PAN, MIDDLE: THREE.MOUSE.DOLLY, RIGHT: THREE.MOUSE.PAN };
    controls.touches = { ONE: THREE.TOUCH.PAN, TWO: THREE.TOUCH.DOLLY_PAN };

    raycaster = new THREE.Raycaster();
    mouse = new THREE.Vector2();

    renderer.domElement.addEventListener('mousedown', onMouseDown);
    renderer.domElement.addEventListener('mousemove', onMouseMove);
    renderer.domElement.addEventListener('mouseup', onMouseUp);
    window.addEventListener('mouseup', onMouseUp); // catch release outside canvas

    window.addEventListener('resize', () => {
      camera.aspect = container.clientWidth / container.clientHeight;
      camera.updateProjectionMatrix();
      renderer.setSize(container.clientWidth, container.clientHeight);
    });

    createStarField();
    createDustField();

    const info = document.createElement('div');
    info.id = 'galaxy-info';
    info.textContent = 'Drag a star to reposition · Drag empty space to pan · Scroll to zoom · Click for details';
    document.body.appendChild(info);

    let frame = 0;
    let lastLabelScale = -1;
    function animate() {
      requestAnimationFrame(animate);
      controls.update();
      frame++;
      // Twinkle stars (cheap: single material update)
      if (starField && frame % 6 === 0) {
        starField.material.opacity = 0.5 + Math.sin(frame * 0.02) * 0.1;
      }
      // Slow dust drift (cheap: single rotation)
      if (dustField) dustField.rotation.z += 0.0003;
      // Label distance scaling — only when camera distance changed significantly
      const camDist = camera.position.distanceTo(controls.target);
      const distScale = Math.max(0.5, Math.min(2.5, camDist / 250));
      if (Math.abs(distScale - lastLabelScale) > 0.01) {
        lastLabelScale = distScale;
        for (const lbl of nodeLabels) {
          if (!lbl.visible) continue;
          lbl.scale.set(lbl.material.map.image.width * 0.12 * distScale, lbl.material.map.image.height * 0.12 * distScale, 1);
        }
      }
      renderer.render(scene, camera);
    }
    animate();
  }

  async function init() {
    try {
      const res = await fetch('/api/graph');
      if (!res.ok) throw new Error(res.statusText);
      const graph = await res.json();
      buildGalaxy(graph);
    } catch (err) {
      console.error(err);
      document.getElementById('cy').textContent = 'Failed to load graph: ' + err.message;
    }
  }

  initThree();
  if (renderer) init();
})();
