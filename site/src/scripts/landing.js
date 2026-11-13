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
    const geometry = new THREE.IcosahedronGeometry(2.8, 1),
      edges = new THREE.EdgesGeometry(geometry),
      material = new THREE.LineBasicMaterial({
        color: 0x38e1ff,
        transparent: true,
        opacity: 0.14,
      });
    const mesh = new THREE.LineSegments(edges, material);
    group.add(mesh);
    group.position.set(2.6, 0.4, -2);
    const nodes = new THREE.Points(
      geometry,
      new THREE.PointsMaterial({
        color: 0x38e1ff,
        size: 0.045,
        transparent: true,
        opacity: 0.5,
      }),
    );
    group.add(nodes);
    const resize = () => {
      renderer.setSize(innerWidth, innerHeight);
      camera.aspect = innerWidth / innerHeight;
      camera.updateProjectionMatrix();
    };
    resize();
    addEventListener("resize", resize);
    let frame = 0;
    const render = (t) => {
      group.rotation.y = t * 0.000045;
      group.rotation.z = 0.15 + Math.sin(t * 0.0001) * 0.1;
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
