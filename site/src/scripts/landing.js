import * as THREE from "three";
const host = document.querySelector("#scene");
const motion = matchMedia("(prefers-reduced-motion: reduce)");
if (host && !motion.matches) {
  try {
    const renderer = new THREE.WebGLRenderer({ alpha: true, antialias: true });
    renderer.setPixelRatio(Math.min(devicePixelRatio, 1.5));
    host.append(renderer.domElement);
    const scene = new THREE.Scene(),
      camera = new THREE.PerspectiveCamera(
        45,
        innerWidth / innerHeight,
        0.1,
        100,
      );
    camera.position.z = 9;
    const group = new THREE.Group();
    scene.add(group);
    const geometry = new THREE.IcosahedronGeometry(5.4, 1),
      edges = new THREE.EdgesGeometry(geometry),
      material = new THREE.LineBasicMaterial({
        color: 0x38e1ff,
        transparent: true,
        opacity: 0.34,
      });
    const mesh = new THREE.LineSegments(edges, material);
    group.add(mesh);
    const shellGeometry = new THREE.IcosahedronGeometry(7.2, 1),
      shellEdges = new THREE.EdgesGeometry(shellGeometry),
      shellMaterial = new THREE.LineBasicMaterial({
        color: 0x7af6ff,
        transparent: true,
        opacity: 0.1,
      });
    const shell = new THREE.LineSegments(shellEdges, shellMaterial);
    group.add(shell);
    const ringGeometry = new THREE.TorusGeometry(6.1, 0.015, 12, 140);
    const ringMaterial = new THREE.MeshBasicMaterial({
      color: 0x38e1ff,
      transparent: true,
      opacity: 0.45,
    });
    const ring = new THREE.Mesh(ringGeometry, ringMaterial);
    ring.rotation.x = Math.PI / 2.6;
    group.add(ring);
    const haloMaterial = ringMaterial.clone();
    haloMaterial.opacity = 0.2;
    const halo = new THREE.Mesh(ringGeometry, haloMaterial);
    halo.rotation.x = Math.PI / 1.7;
    halo.rotation.y = 0.6;
    halo.scale.setScalar(1.18);
    group.add(halo);
    group.position.set(0, 0.15, -2.4);
    // The right column is an opaque code panel, so a positive x hides the
    // sphere. Keep its center in the open left third of whatever viewport.
    const place = () => {
      const distance = camera.position.z - group.position.z;
      const halfWidth =
        Math.tan((camera.fov * Math.PI) / 360) * distance * camera.aspect;
      group.position.x = -halfWidth * 0.55;
    };
    const nodes = new THREE.Points(
      geometry,
      new THREE.PointsMaterial({
        color: 0xb8fbff,
        size: 0.07,
        transparent: true,
        opacity: 0.85,
      }),
    );
    group.add(nodes);
    const resize = () => {
      renderer.setSize(innerWidth, innerHeight);
      camera.aspect = innerWidth / innerHeight;
      camera.updateProjectionMatrix();
      place();
    };
    resize();
    addEventListener("resize", resize);
    let frame = 0;
    const render = (t) => {
      group.rotation.y = t * 0.00072;
      group.rotation.x = Math.sin(t * 0.00031) * 0.45;
      group.rotation.z = 0.25 + Math.sin(t * 0.00024) * 0.28;
      ring.rotation.z = t * 0.0009;
      halo.rotation.z = t * -0.00055;
      renderer.render(scene, camera);
      frame = requestAnimationFrame(render);
    };
    const visibility = () => {
      cancelAnimationFrame(frame);
      if (!document.hidden) frame = requestAnimationFrame(render);
    };
    document.addEventListener("visibilitychange", visibility);
    visibility();
    const cleanup = () => {
      cancelAnimationFrame(frame);
      removeEventListener("resize", resize);
      document.removeEventListener("visibilitychange", visibility);
      geometry.dispose();
      edges.dispose();
      material.dispose();
      shellGeometry.dispose();
      shellEdges.dispose();
      shellMaterial.dispose();
      ringGeometry.dispose();
      ringMaterial.dispose();
      haloMaterial.dispose();
      nodes.material.dispose();
      renderer.dispose();
      renderer.domElement.remove();
    };
    addEventListener("pagehide", cleanup, { once: true });
  } catch {
    host.dataset.fallback = "true";
  }
}

const featureTabs = [...document.querySelectorAll(".showcase [role='tab']")];
const featureNote = document.querySelector("#feature-note");
function selectFeature(tab) {
  for (const item of featureTabs) {
    const selected = item === tab;
    item.setAttribute("aria-selected", selected ? "true" : "false");
    item.tabIndex = selected ? 0 : -1;
    const panel = document.getElementById(item.getAttribute("aria-controls"));
    if (panel) panel.hidden = !selected;
  }
  if (featureNote && tab.dataset.note) {
    const mark = featureNote.querySelector(".status");
    featureNote.textContent = tab.dataset.note;
    if (mark) featureNote.prepend(mark);
  }
}
for (const tab of featureTabs) {
  tab.addEventListener("click", () => selectFeature(tab));
  tab.addEventListener("keydown", (event) => {
    const index = featureTabs.indexOf(tab);
    const last = featureTabs.length - 1;
    const next =
      event.key === "ArrowRight"
        ? featureTabs[(index + 1) % featureTabs.length]
        : event.key === "ArrowLeft"
          ? featureTabs[(index + last) % featureTabs.length]
          : event.key === "Home"
            ? featureTabs[0]
            : event.key === "End"
              ? featureTabs[last]
              : null;
    if (!next) return;
    event.preventDefault();
    selectFeature(next);
    next.focus();
  });
}
