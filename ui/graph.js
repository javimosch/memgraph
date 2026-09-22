import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';

const searchInput = document.getElementById('search');
const tooltip = document.getElementById('tooltip');
const panel = document.getElementById('panel');
const panelContent = document.getElementById('panel-content');
const closeBtn = document.getElementById('panel-close');
const copyBtn = document.getElementById('copy-md');
const copyVisibleBtn = document.getElementById('copy-visible-paths');
const syncBtn = document.getElementById('sync-btn');

let scene, camera, renderer, controls;
let nodeMeshes = [], glowPoints = null, glowData = [], nodeLabels = [], edgeLines = [], allEdges = [], nodeMap = {};
let raycaster, mouse, hoveredNode = null;
let currentMarkdown = '';
let starField = null, dustField = null, glowTexture = null;
const labelTextureCache = {};

function makeGlowTexture() {
  if (glowTexture) return glowTexture;
  const size = 64;
  const canvas = document.createElement('canvas');
  canvas.width = size; canvas.height = size;
  const ctx = canvas.getContext('2d');
  const grad = ctx.createRadialGradient(size/2, size/2, 0, size/2, size/2, size/2);
  grad.addColorStop(0, 'rgba(255,255,255,1)');
  grad.addColorStop(0.2, 'rgba(255,255,255,0.6)');
  grad.addColorStop(0.5, 'rgba(255,255,255,0.15)');
  grad.addColorStop(1, 'rgba(255,255,255,0)');
  ctx.fillStyle = grad; ctx.fillRect(0, 0, size, size);
  glowTexture = new THREE.CanvasTexture(canvas);
  glowTexture.minFilter = THREE.LinearFilter;
  glowTexture.magFilter = THREE.LinearFilter;
  return glowTexture;
}

function projectColor(project) {
  if (!project) return { r: 0.5, g: 0.69, b: 0.41 };
  let h = 0;
  for (let i = 0; i < project.length; i++) h = (h * 31 + project.charCodeAt(i)) % 360;
  const c = new THREE.Color(); c.setHSL(h / 360, 0.7, 0.55);
  return { r: c.r, g: c.g, b: c.b };
}

function makeLabelTexture(text, fontSize, color) {
  const key = `${fontSize}:${color}:${text}`;
  if (labelTextureCache[key]) return labelTextureCache[key];
  const canvas = document.createElement('canvas');
  const ctx = canvas.getContext('2d');
  ctx.font = `${fontSize}px system-ui, sans-serif`;
  const metrics = ctx.measureText(text);
  const w = Math.ceil(metrics.width) + 8, h = fontSize + 6;
  canvas.width = w; canvas.height = h;
  ctx.font = `${fontSize}px system-ui, sans-serif`;
  ctx.fillStyle = 'rgba(0,0,0,0)';
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
  const parts = md.split('```');
  let html = '';
  for (let i = 0; i < parts.length; i++) {
    if (i % 2 === 1) html += `<pre><code>${escapeHtml(parts[i])}</code></pre>`;
    else html += `<p>${escapeHtml(parts[i]).replace(/\n/g, '<br>')}</p>`;
  }
  return html;
}

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

  const pos = {}, numArms = 4, armSpacing = (Math.PI * 2) / numArms;
  const galaxyRadius = 40 + Math.sqrt(list.length) * 35;
  const coreRadius = 60;

  for (let i = 0; i < list.length; i++) {
    const g = list[i];
    const arm = i % numArms;
    const t = (i + 1) / list.length;
    const distFromCore = coreRadius + t * galaxyRadius;
    const angle = arm * armSpacing + t * 2.2 + Math.sin(i * 7.13) * 0.15;
    const cx = Math.cos(angle) * distFromCore;
    const cy = Math.sin(angle) * distFromCore;
    if (g.hub) pos[g.hub.id] = { x: cx, y: cy };
    const golden = 2.39996322972865332, spacing = 14;
    for (let k = 0; k < g.members.length; k++) {
      const r = 10 + spacing * Math.sqrt(k);
      const a = k * golden + i * 0.3;
      pos[g.members[k].id] = { x: cx + Math.cos(a) * r, y: cy + Math.sin(a) * r };
    }
  }
  return pos;
}

function buildGalaxy(graph) {
  allEdges = graph.edges || [];
  const pos = galaxyLayout(graph);
  const degrees = {};
  for (const n of graph.nodes) degrees[n.id] = 0;
  for (const e of allEdges) { degrees[e.source] = (degrees[e.source]||0)+1; degrees[e.target] = (degrees[e.target]||0)+1; }

  nodeMeshes = []; glowData = []; nodeLabels = []; nodeMap = {};
  const glowPositions = [], glowColors = [], glowSizes = [];

  for (const n of graph.nodes) {
    const p = pos[n.id] || { x: 0, y: 0 };
    const isNs = n.type === 'namespace';
    const col = isNs ? { r: 1.0, g: 0.85, b: 0.4 } : projectColor(n.project);
    const color = new THREE.Color(col.r, col.g, col.b);
    const size = isNs ? 3.5 : 1.2 + Math.min(degrees[n.id] || 0, 20) * 0.12;

    const geo = new THREE.SphereGeometry(size, isNs ? 12 : 6, isNs ? 12 : 6);
    const mat = new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 1.0 });
    const mesh = new THREE.Mesh(geo, mat);
    mesh.position.set(p.x, p.y, 0);
    mesh.userData = { id: n.id, name: n.name, type: n.type, project: n.project, tags: n.tags, file_path: n.file_path, degree: degrees[n.id]||0, color, size, visible: true, baseSize: size };
    scene.add(mesh); nodeMeshes.push(mesh); nodeMap[n.id] = mesh;

    glowPositions.push(p.x, p.y, 0);
    glowColors.push(col.r, col.g, col.b);
    glowSizes.push(isNs ? 18 : 10);
    glowData.push({ nodeId: n.id, visible: true, baseOpacity: isNs ? 0.4 : 0.25 });

    const label = makeLabel(n.name || n.id, isNs ? 44 : 28, isNs ? '#ffe9a8' : '#a8c8ff');
    label.position.set(p.x, p.y + size + 4, 1);
    label.userData = { nodeId: n.id, visible: true };
    scene.add(label); nodeLabels.push(label);
  }

  const glowGeo = new THREE.BufferGeometry();
  glowGeo.setAttribute('position', new THREE.Float32BufferAttribute(glowPositions, 3));
  glowGeo.setAttribute('color', new THREE.Float32BufferAttribute(glowColors, 3));
  const glowMat = new THREE.PointsMaterial({ size: 20, map: makeGlowTexture(), vertexColors: true, transparent: true, opacity: 0.35, sizeAttenuation: true, blending: THREE.AdditiveBlending, depthWrite: false });
  glowPoints = new THREE.Points(glowGeo, glowMat);
  scene.add(glowPoints);

  const edgePositions = [], edgeColors = [], edgeMeta = [];
  for (const e of allEdges) {
    if (e.relation === 'shared-keyword') continue;
    const s = pos[e.source], t = pos[e.target];
    if (!s || !t) continue;
    edgePositions.push(s.x, s.y, 0, t.x, t.y, 0);
    let col = e.relation === 'namespace' ? [0.6,0.5,0.3] : e.relation === 'references' ? [0.3,0.5,0.7] : e.relation === 'similar' ? [0.4,0.6,0.5] : [0.5,0.6,0.8];
    edgeColors.push(...col, ...col);
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
  let minX=Infinity, maxX=-Infinity, minY=Infinity, maxY=-Infinity;
  const nodes = targets || nodeMeshes;
  for (const m of nodes) {
    if (!m.userData.visible) continue;
    const p = m.position;
    if (p.x<minX) minX=p.x; if (p.x>maxX) maxX=p.x; if (p.y<minY) minY=p.y; if (p.y>maxY) maxY=p.y;
  }
  if (minX===Infinity) { minX=-100; maxX=100; minY=-100; maxY=100; }
  const cx=(minX+maxX)/2, cy=(minY+maxY)/2;
  const dist = Math.max(maxX-minX, maxY-minY) * 0.6 + 80;
  controls.target.set(cx, cy, 0);
  camera.position.set(cx, cy, dist);
  controls.update();
}

function rebuildEdgeGeometry() {
  const positions = [], colors = [];
  for (const el of edgeLines) {
    for (const m of el.meta) {
      if (!m.visible) continue;
      const s = nodeMap[m.source], t = nodeMap[m.target];
      if (!s || !t || !s.userData.visible || !t.userData.visible) continue;
      positions.push(s.position.x, s.position.y, 0, t.position.x, t.position.y, 0);
      let col = m.relation==='namespace' ? [0.6,0.5,0.3] : m.relation==='references' ? [0.3,0.5,0.7] : m.relation==='similar' ? [0.4,0.6,0.5] : [0.5,0.6,0.8];
      colors.push(...col, ...col);
    }
    el.geo.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
    el.geo.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));
    el.geo.attributes.position.needsUpdate = true;
  }
}

function toggleEdge(type, show) {
  for (const el of edgeLines) for (const m of el.meta) if (m.relation === type) m.visible = show;
  rebuildEdgeGeometry();
}

function updateFilter() {
  const q = searchInput.value.trim().toLowerCase();
  const visibleIds = new Set();
  for (const m of nodeMeshes) {
    const d = m.userData;
    if (!q) { d.visible = true; m.visible = true; visibleIds.add(d.id); continue; }
    d.visible = (d.name||'').toLowerCase().includes(q) || (d.project||'').toLowerCase().includes(q) || (d.tags||[]).some(t => t.includes(q));
    m.visible = d.visible;
    if (d.visible) visibleIds.add(d.id);
  }
  for (const lbl of nodeLabels) lbl.visible = visibleIds.has(lbl.userData.nodeId);
  for (const gd of glowData) gd.visible = visibleIds.has(gd.nodeId);
  rebuildEdgeGeometry();
}

function showPanel(id) {
  panel.classList.remove('hidden');
  const node = nodeMap[id];
  const d = node ? node.userData : {};
  panelContent.innerHTML = `<h2>${escapeHtml(d.name || id)}</h2><div class="meta"><span>project: ${escapeHtml(d.project||'?')}</span><span>type: ${escapeHtml(d.type||'?')}</span><span>degree: ${d.degree||0}</span></div><p>Loading…</p>`;
  currentMarkdown = '';
  fetch(`/api/nodes/${encodeURIComponent(id)}`)
    .then(r => { if (!r.ok) throw new Error(r.statusText); return r.text(); })
    .then(md => { currentMarkdown = md; panelContent.innerHTML = `<h2>${escapeHtml(d.name || id)}</h2><div class="meta"><span>project: ${escapeHtml(d.project||'?')}</span><span>type: ${escapeHtml(d.type||'?')}</span><span>degree: ${d.degree||0}</span></div>${renderMarkdown(md)}`; })
    .catch(err => { panelContent.innerHTML = `<h2>${escapeHtml(d.name || id)}</h2><div class="meta"><span>project: ${escapeHtml(d.project||'?')}</span></div><p style="color:#c77">Error: ${escapeHtml(err.message)}</p>`; });
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
searchInput.addEventListener('input', updateFilter);
searchInput.addEventListener('keydown', e => { if (e.key === 'Enter') { const q = searchInput.value.trim().toLowerCase(); if (q) { const t = nodeMeshes.filter(m => m.userData.visible); if (t.length) fitCamera(t); } else fitCamera(); } });
document.querySelectorAll('.edge-toggle').forEach(cb => cb.addEventListener('change', () => toggleEdge(cb.value, cb.checked)));

copyVisibleBtn.addEventListener('click', () => {
  const paths = nodeMeshes.filter(m => m.userData.visible && m.userData.file_path).map(m => m.userData.file_path).join('\n');
  navigator.clipboard.writeText(paths).catch(() => {});
  const orig = copyVisibleBtn.textContent;
  copyVisibleBtn.textContent = 'Copied!';
  setTimeout(() => copyVisibleBtn.textContent = orig, 1200);
});

syncBtn.addEventListener('click', async () => {
  syncBtn.textContent = 'Syncing…';
  try {
    const res = await fetch('/api/sync', { method: 'POST' });
    if (res.ok) {
      const graph = await fetch('/api/graph').then(r => r.json());
      buildGalaxy(graph);
      syncBtn.textContent = 'Synced!';
    } else {
      syncBtn.textContent = 'Sync failed';
    }
  } catch (e) {
    syncBtn.textContent = 'Sync failed';
  }
  setTimeout(() => syncBtn.textContent = 'Sync', 1500);
});

// Drag + click
let draggedNode = null, dragMoved = false, dragStartPos = null;
const dragPlane = new THREE.Plane(new THREE.Vector3(0,0,1), 0);
const dragIntersect = new THREE.Vector3();

function screenToWorld(event) {
  const rect = renderer.domElement.getBoundingClientRect();
  raycaster.setFromCamera(new THREE.Vector2(((event.clientX-rect.left)/rect.width)*2-1, -((event.clientY-rect.top)/rect.height)*2+1), camera);
  raycaster.ray.intersectPlane(dragPlane, dragIntersect);
  return dragIntersect.clone();
}

function onMouseDown(event) {
  if (event.button !== 0) return;
  const rect = renderer.domElement.getBoundingClientRect();
  mouse.x = ((event.clientX-rect.left)/rect.width)*2-1;
  mouse.y = -((event.clientY-rect.top)/rect.height)*2+1;
  raycaster.setFromCamera(mouse, camera);
  const intersects = raycaster.intersectObjects(nodeMeshes.filter(m => m.visible));
  if (intersects.length > 0) {
    draggedNode = intersects[0].object;
    dragMoved = false;
    dragStartPos = { x: event.clientX, y: event.clientY };
    controls.enabled = false;
    renderer.domElement.style.cursor = 'grabbing';
  }
}

function onMouseMove(event) {
  if (draggedNode) {
    const dx = event.clientX - dragStartPos.x, dy = event.clientY - dragStartPos.y;
    if (!dragMoved && Math.hypot(dx, dy) > 4) dragMoved = true;
    if (dragMoved) {
      const world = screenToWorld(event);
      draggedNode.position.set(world.x, world.y, 0);
      const idx = nodeMeshes.indexOf(draggedNode);
      if (glowPoints && idx >= 0) { const arr = glowPoints.geometry.attributes.position.array; arr[idx*3]=world.x; arr[idx*3+1]=world.y; glowPoints.geometry.attributes.position.needsUpdate = true; }
      const lbl = nodeLabels.find(l => l.userData.nodeId === draggedNode.userData.id);
      if (lbl) lbl.position.set(world.x, world.y + draggedNode.userData.size + 4, 1);
      rebuildEdgeGeometry();
    }
    return;
  }
  const rect = renderer.domElement.getBoundingClientRect();
  mouse.x = ((event.clientX-rect.left)/rect.width)*2-1;
  mouse.y = -((event.clientY-rect.top)/rect.height)*2+1;
  raycaster.setFromCamera(mouse, camera);
  const intersects = raycaster.intersectObjects(nodeMeshes.filter(m => m.visible));
  if (intersects.length > 0) {
    const node = intersects[0].object;
    if (hoveredNode !== node) { if (hoveredNode) hoveredNode.material.opacity = 1.0; hoveredNode = node; hoveredNode.material.opacity = 1.5; tooltip.style.display = 'block'; tooltip.textContent = node.userData.name || node.userData.id; }
    tooltip.style.left = (event.clientX + 12) + 'px';
    tooltip.style.top = (event.clientY + 12) + 'px';
    renderer.domElement.style.cursor = 'pointer';
  } else {
    if (hoveredNode) { hoveredNode.material.opacity = 1.0; hoveredNode = null; }
    tooltip.style.display = 'none';
    renderer.domElement.style.cursor = 'grab';
  }
}

function onMouseUp() {
  if (draggedNode) {
    if (!dragMoved) showPanel(draggedNode.userData.id);
    draggedNode = null; dragMoved = false;
    controls.enabled = true;
    renderer.domElement.style.cursor = 'grab';
  }
}

function createStarField() {
  const count = 1500;
  const geo = new THREE.BufferGeometry();
  const positions = new Float32Array(count * 3), colors = new Float32Array(count * 3);
  for (let i = 0; i < count; i++) {
    const r = 200 + Math.random() * 1800, a = Math.random() * Math.PI * 2;
    positions[i*3] = Math.cos(a)*r; positions[i*3+1] = Math.sin(a)*r; positions[i*3+2] = (Math.random()-0.5)*50;
    const intensity = 0.3 + Math.random() * 0.7, tint = Math.random();
    if (tint < 0.6) { colors[i*3]=intensity; colors[i*3+1]=intensity; colors[i*3+2]=intensity; }
    else if (tint < 0.85) { colors[i*3]=intensity*0.7; colors[i*3+1]=intensity*0.85; colors[i*3+2]=intensity; }
    else { colors[i*3]=intensity; colors[i*3+1]=intensity*0.8; colors[i*3+2]=intensity*0.5; }
  }
  geo.setAttribute('position', new THREE.BufferAttribute(positions, 3));
  geo.setAttribute('color', new THREE.BufferAttribute(colors, 3));
  starField = new THREE.Points(geo, new THREE.PointsMaterial({ size: 1.2, vertexColors: true, transparent: true, opacity: 0.6, sizeAttenuation: true, blending: THREE.AdditiveBlending }));
  scene.add(starField);
}

function createDustField() {
  const count = 400;
  const geo = new THREE.BufferGeometry();
  const positions = new Float32Array(count * 3), colors = new Float32Array(count * 3);
  for (let i = 0; i < count; i++) {
    const r = 30 + Math.random() * 400, a = Math.random() * Math.PI * 2;
    positions[i*3] = Math.cos(a)*r; positions[i*3+1] = Math.sin(a)*r; positions[i*3+2] = (Math.random()-0.5)*10;
    const intensity = 0.05 + Math.random() * 0.1;
    colors[i*3]=intensity*0.6; colors[i*3+1]=intensity*0.7; colors[i*3+2]=intensity;
  }
  geo.setAttribute('position', new THREE.BufferAttribute(positions, 3));
  geo.setAttribute('color', new THREE.BufferAttribute(colors, 3));
  dustField = new THREE.Points(geo, new THREE.PointsMaterial({ size: 8, vertexColors: true, transparent: true, opacity: 0.4, sizeAttenuation: true, blending: THREE.AdditiveBlending, depthWrite: false }));
  scene.add(dustField);
}

// Init
const container = document.getElementById('cy');
scene = new THREE.Scene();
scene.background = new THREE.Color(0x000005);
camera = new THREE.PerspectiveCamera(50, container.clientWidth / container.clientHeight, 0.1, 5000);
camera.position.set(0, 0, 400);

try {
  renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false });
} catch (e) {
  container.innerHTML = '<div style="color:#aaccff;padding:2rem">WebGL not available.</div>';
  throw e;
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
window.addEventListener('mouseup', onMouseUp);
window.addEventListener('resize', () => {
  camera.aspect = container.clientWidth / container.clientHeight;
  camera.updateProjectionMatrix();
  renderer.setSize(container.clientWidth, container.clientHeight);
});

createStarField();
createDustField();

// Load live graph data from the API
fetch('/api/graph')
  .then(r => { if (!r.ok) throw new Error(r.statusText); return r.json(); })
  .then(graph => buildGalaxy(graph))
  .catch(err => { container.textContent = 'Failed to load graph: ' + err.message; });

const info = document.createElement('div');
info.id = 'galaxy-info';
info.textContent = 'Drag a star to reposition · Drag empty space to pan · Scroll to zoom · Click for details';
document.body.appendChild(info);

let frame = 0, lastLabelScale = -1;
function animate() {
  requestAnimationFrame(animate);
  controls.update();
  frame++;
  if (starField && frame % 6 === 0) starField.material.opacity = 0.5 + Math.sin(frame * 0.02) * 0.1;
  if (dustField) dustField.rotation.z += 0.0003;
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
