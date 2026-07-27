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
  let nodeLabels = [];
  let edgeLines = [];
  let allEdges = [];
  let nodeData = [];
  let nodeMap = {};
  let currentMarkdown = '';
  let raycaster, mouse;
  let hoveredNode = null;
  let labelEl;

  function projectColor(project) {
    if (!project) return 0x7fb069;
    let h = 0;
    for (let i = 0; i < project.length; i++) h = (h * 31 + project.charCodeAt(i)) % 360;
    const c = new THREE.Color();
    c.setHSL(h / 360, 0.65, 0.48);
    return c.getHex();
  }

  const labelTextureCache = {};
  function makeLabelTexture(text, fontSize, color) {
    const key = `${fontSize}:${color}:${text}`;
    if (labelTextureCache[key]) return labelTextureCache[key];

    const canvas = document.createElement('canvas');
    const ctx = canvas.getContext('2d');
    ctx.font = `${fontSize}px system-ui, sans-serif`;
    const metrics = ctx.measureText(text);
    const w = Math.ceil(metrics.width) + 6;
    const h = fontSize + 4;
    canvas.width = w;
    canvas.height = h;
    ctx.font = `${fontSize}px system-ui, sans-serif`;
    ctx.fillStyle = 'rgba(15, 26, 18, 0.7)';
    ctx.fillRect(0, 0, w, h);
    ctx.fillStyle = color;
    ctx.textBaseline = 'middle';
    ctx.fillText(text, 3, h / 2);

    const tex = new THREE.CanvasTexture(canvas);
    tex.minFilter = THREE.LinearFilter;
    tex.magFilter = THREE.LinearFilter;
    labelTextureCache[key] = tex;
    return tex;
  }

  function makeLabel(text, fontSize, colorHex) {
    const tex = makeLabelTexture(text, fontSize, colorHex);
    const mat = new THREE.SpriteMaterial({ map: tex, transparent: true, depthTest: false });
    const sprite = new THREE.Sprite(mat);
    sprite.scale.set(tex.image.width * 0.15, tex.image.height * 0.15, 1);
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

  function seedLayout3D(graph) {
    const groups = {};
    for (const n of graph.nodes) {
      const p = n.project || 'general';
      if (!groups[p]) groups[p] = { project: p, hub: null, members: [] };
      if (n.type === 'namespace') groups[p].hub = n;
      else groups[p].members.push(n);
    }

    const golden = 2.39996322972865332;
    const spacing = 30;
    const list = Object.values(groups).map(g => {
      g.members.sort((a, b) => a.name.localeCompare(b.name));
      const outerR = 40 + spacing * Math.sqrt(Math.max(1, g.members.length));
      g.bounding = outerR + 40;
      return g;
    }).sort((a, b) => b.bounding - a.bounding);

    const pos = {};
    const placed = [];

    for (let i = 0; i < list.length; i++) {
      const g = list[i];
      let gx = 0, gy = 0, gz = 0;
      if (i > 0) {
        let minSep = 0;
        for (const p of placed) minSep = Math.max(minSep, p.bounding + g.bounding + 30);
        let theta = i * golden, phi = i * golden * 0.5, dist = minSep;
        for (let attempt = 0; attempt < 8000; attempt++) {
          gx = Math.sin(phi) * Math.cos(theta) * dist;
          gy = Math.sin(phi) * Math.sin(theta) * dist;
          gz = Math.cos(phi) * dist;
          let overlap = false;
          for (const p of placed) {
            const d = Math.hypot(gx - p.cx, gy - p.cy, gz - p.cz);
            if (d < p.bounding + g.bounding) { overlap = true; break; }
          }
          if (!overlap) break;
          theta += 0.15;
          phi += 0.08;
          dist += 2.5;
        }
      }
      g.cx = gx; g.cy = gy; g.cz = gz;
      placed.push(g);

      if (g.hub) pos[g.hub.id] = { x: gx, y: gy, z: gz };

      for (let k = 0; k < g.members.length; k++) {
        const r = 36 + spacing * Math.sqrt(k);
        const a = k * golden;
        const zOff = Math.sin(k * golden * 0.5) * spacing * 0.3;
        const m = g.members[k];
        pos[m.id] = {
          x: gx + Math.cos(a) * r,
          y: gy + Math.sin(a) * r,
          z: gz + zOff
        };
      }
    }
    return pos;
  }

  function buildGraph3D(graph) {
    allEdges = graph.edges || [];
    const pos = seedLayout3D(graph);

    const degrees = {};
    for (const n of graph.nodes) degrees[n.id] = 0;
    for (const e of allEdges) {
      degrees[e.source] = (degrees[e.source] || 0) + 1;
      degrees[e.target] = (degrees[e.target] || 0) + 1;
    }

    nodeData = [];
    nodeMap = {};
    nodeLabels = [];

    for (const n of graph.nodes) {
      const p = pos[n.id] || { x: 0, y: 0, z: 0 };
      const isNs = n.type === 'namespace';
      const color = isNs ? 0x8b5a2b : projectColor(n.project);
      const size = isNs ? 6 : 2 + Math.min(degrees[n.id] || 0, 20) * 0.15;

      const geo = new THREE.SphereGeometry(size, isNs ? 16 : 8, isNs ? 16 : 8);
      const mat = new THREE.MeshPhongMaterial({ color, shininess: 40, transparent: true, opacity: 0.9 });
      const mesh = new THREE.Mesh(geo, mat);
      mesh.position.set(p.x, p.y, p.z);
      mesh.userData = {
        id: n.id, name: n.name, type: n.type, project: n.project,
        tags: n.tags, file_path: n.file_path, degree: degrees[n.id] || 0,
        color, size, visible: true
      };
      scene.add(mesh);
      nodeMeshes.push(mesh);
      nodeMap[n.id] = mesh;
      nodeData.push(mesh.userData);

      const labelText = n.name || n.id;
      const labelFontSize = isNs ? 48 : 32;
      const labelColor = isNs ? '#e8f0e8' : '#c8d8c8';
      const label = makeLabel(labelText, labelFontSize, labelColor);
      label.position.set(p.x, p.y + size + 3, p.z);
      label.userData = { nodeId: n.id, visible: true };
      scene.add(label);
      nodeLabels.push(label);
    }

    const edgeGeo = new THREE.BufferGeometry();
    const positions = [];
    const colors = [];
    const edgeMeta = [];

    for (const e of allEdges) {
      if (e.relation === 'shared-keyword') continue;
      const s = pos[e.source], t = pos[e.target];
      if (!s || !t) continue;
      positions.push(s.x, s.y, s.z, t.x, t.y, t.z);
      let col;
      if (e.relation === 'namespace') col = [0.55, 0.35, 0.17];
      else if (e.relation === 'references') col = [0.5, 0.69, 0.41];
      else if (e.relation === 'similar') col = [0.43, 0.6, 0.37];
      else col = [0.66, 0.78, 0.54];
      colors.push(col[0], col[1], col[2], col[0], col[1], col[2]);
      edgeMeta.push({ source: e.source, target: e.target, relation: e.relation, visible: e.relation !== 'similar' });
    }

    edgeGeo.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
    edgeGeo.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));
    const edgeMat = new THREE.LineBasicMaterial({ vertexColors: true, transparent: true, opacity: 0.3 });
    const edgeLine = new THREE.LineSegments(edgeGeo, edgeMat);
    scene.add(edgeLine);
    edgeLines.push({ line: edgeLine, meta: edgeMeta, geo: edgeGeo });

    fitCamera();
  }

  function fitCamera(targets) {
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity, minZ = Infinity, maxZ = -Infinity;
    const nodes = targets || nodeMeshes;
    for (const m of nodes) {
      if (!m.userData.visible) continue;
      const p = m.position;
      if (p.x < minX) minX = p.x; if (p.x > maxX) maxX = p.x;
      if (p.y < minY) minY = p.y; if (p.y > maxY) maxY = p.y;
      if (p.z < minZ) minZ = p.z; if (p.z > maxZ) maxZ = p.z;
    }
    if (minX === Infinity) { minX = -10; maxX = 10; minY = -10; maxY = 10; minZ = -10; maxZ = 10; }
    const cx = (minX + maxX) / 2, cy = (minY + maxY) / 2, cz = (minZ + maxZ) / 2;
    const dx = maxX - minX, dy = maxY - minY, dz = maxZ - minZ;
    const dist = Math.max(dx, dy, dz) * 0.7 + 50;
    controls.target.set(cx, cy, cz);
    camera.position.set(cx + dist * 0.3, cy + dist * 0.3, cz + dist);
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
    const pos = nodeMap;
    for (const el of edgeLines) {
      const positions = [];
      const colors = [];
      for (const m of el.meta) {
        if (!m.visible) continue;
        const s = pos[m.source], t = pos[m.target];
        if (!s || !t) continue;
        if (!s.userData.visible || !t.userData.visible) continue;
        positions.push(s.position.x, s.position.y, s.position.z, t.position.x, t.position.y, t.position.z);
        let col;
        if (m.relation === 'namespace') col = [0.55, 0.35, 0.17];
        else if (m.relation === 'references') col = [0.5, 0.69, 0.41];
        else if (m.relation === 'similar') col = [0.43, 0.6, 0.37];
        else col = [0.66, 0.78, 0.54];
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
    for (const m of nodeMeshes) {
      const d = m.userData;
      if (!q) { d.visible = true; m.visible = true; continue; }
      const inName = (d.name || '').toLowerCase().includes(q);
      const inProject = (d.project || '').toLowerCase().includes(q);
      const inTags = (d.tags || []).some(t => t.toLowerCase().includes(q));
      d.visible = inName || inProject || inTags;
      m.visible = d.visible;
    }
    for (const lbl of nodeLabels) {
      lbl.visible = nodeMap[lbl.userData.nodeId] && nodeMap[lbl.userData.nodeId].visible;
    }
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

  function onMouseMove(event) {
    const rect = renderer.domElement.getBoundingClientRect();
    mouse.x = ((event.clientX - rect.left) / rect.width) * 2 - 1;
    mouse.y = -((event.clientY - rect.top) / rect.height) * 2 + 1;

    raycaster.setFromCamera(mouse, camera);
    const visible = nodeMeshes.filter(m => m.visible);
    const intersects = raycaster.intersectObjects(visible);

    if (intersects.length > 0) {
      const node = intersects[0].object;
      if (hoveredNode !== node) {
        if (hoveredNode) hoveredNode.material.emissive.setHex(0x000000);
        hoveredNode = node;
        hoveredNode.material.emissive.setHex(0x334422);
        tooltip.classList.remove('hidden');
        tooltip.textContent = node.userData.name || node.userData.id;
      }
      tooltip.style.left = (event.clientX + 12) + 'px';
      tooltip.style.top = (event.clientY + 12) + 'px';
      renderer.domElement.style.cursor = 'pointer';
    } else {
      if (hoveredNode) { hoveredNode.material.emissive.setHex(0x000000); hoveredNode = null; }
      tooltip.classList.add('hidden');
      renderer.domElement.style.cursor = 'grab';
    }
  }

  function onClick(event) {
    const rect = renderer.domElement.getBoundingClientRect();
    mouse.x = ((event.clientX - rect.left) / rect.width) * 2 - 1;
    mouse.y = -((event.clientY - rect.top) / rect.height) * 2 + 1;
    raycaster.setFromCamera(mouse, camera);
    const visible = nodeMeshes.filter(m => m.visible);
    const intersects = raycaster.intersectObjects(visible);
    if (intersects.length > 0) {
      showPanel(intersects[0].object.userData.id);
    } else {
      panel.classList.add('hidden');
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
    nodeMeshes = []; nodeLabels = []; edgeLines = []; nodeData = []; nodeMap = {};
  }

  function initThree() {
    const container = document.getElementById('cy');
    scene = new THREE.Scene();
    scene.fog = new THREE.FogExp2(0x050a08, 0.0015);

    camera = new THREE.PerspectiveCamera(60, container.clientWidth / container.clientHeight, 0.1, 5000);
    camera.position.set(0, 0, 300);

    try {
      renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
    } catch (e) {
      container.innerHTML = '<div style="color:#e8f0e8;padding:2rem;font-family:system-ui">WebGL not available. Use a browser with WebGL support to view the 3D graph. <a href="/" style="color:#7fb069">Go to 2D version</a></div>';
      return false;
    }
    renderer.setSize(container.clientWidth, container.clientHeight);
    renderer.setPixelRatio(window.devicePixelRatio);
    container.appendChild(renderer.domElement);

    controls = new OrbitControls(camera, renderer.domElement);
    controls.enableDamping = true;
    controls.dampingFactor = 0.08;

    scene.add(new THREE.AmbientLight(0x445544, 0.5));
    const dl = new THREE.DirectionalLight(0xffffff, 0.6);
    dl.position.set(100, 100, 200);
    scene.add(dl);
    const dl2 = new THREE.DirectionalLight(0x88aa88, 0.3);
    dl2.position.set(-100, -50, -100);
    scene.add(dl2);

    raycaster = new THREE.Raycaster();
    raycaster.params.Points.threshold = 5;
    mouse = new THREE.Vector2();

    renderer.domElement.addEventListener('mousemove', onMouseMove);
    renderer.domElement.addEventListener('click', onClick);

    window.addEventListener('resize', () => {
      camera.aspect = container.clientWidth / container.clientHeight;
      camera.updateProjectionMatrix();
      renderer.setSize(container.clientWidth, container.clientHeight);
    });

    function animate() {
      requestAnimationFrame(animate);
      controls.update();
      scene.rotation.y += 0.0005;
      const camDist = camera.position.distanceTo(controls.target);
      for (const lbl of nodeLabels) {
        if (!lbl.visible) continue;
        const baseScale = 0.15;
        const distScale = Math.max(0.4, Math.min(2.5, camDist / 200));
        lbl.scale.set(lbl.material.map.image.width * baseScale * distScale, lbl.material.map.image.height * baseScale * distScale, 1);
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
      buildGraph3D(graph);
    } catch (err) {
      console.error(err);
      document.getElementById('cy').textContent = 'Failed to load graph: ' + err.message;
    }
  }

  initThree();
  if (renderer) init();
})();
