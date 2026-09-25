import * as THREE from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import { OrbitControls } from "three/addons/controls/OrbitControls.js";
import { RoomEnvironment } from "three/addons/environments/RoomEnvironment.js";
const canvas = document.querySelector("canvas"), status = document.querySelector("#status");
const renderer = new THREE.WebGLRenderer({ canvas, antialias: true });
renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
renderer.toneMapping = THREE.AgXToneMapping;
const scene = new THREE.Scene(); scene.background = new THREE.Color("#182128");
const pmrem = new THREE.PMREMGenerator(renderer), room = new RoomEnvironment();
scene.environment = pmrem.fromScene(room).texture; room.dispose(); pmrem.dispose();
const camera = new THREE.PerspectiveCamera(35, 1, .001, 100);
const controls = new OrbitControls(camera, canvas); controls.enableDamping = true;
let model, size = 1, loading = false;
const center = new THREE.Vector3();
function reset() { controls.target.copy(center); camera.position.copy(center).add(new THREE.Vector3(size * 1.05, size * .9, size * 1.85)); controls.update(); }
async function load(product) {
  if (loading) return; loading = true; status.textContent = "Loading actual exported file…";
  try {
    const gltf = await new GLTFLoader().loadAsync(`/aura-${product}.glb`);
    if (model) { scene.remove(model); model.traverse(o => { if (o.isMesh) { o.geometry.dispose(); const materials = Array.isArray(o.material) ? o.material : [o.material]; for (const m of materials) { for (const value of Object.values(m)) if (value?.isTexture) value.dispose(); m.dispose(); } } }); }
    model = gltf.scene; scene.add(model);
    const box = new THREE.Box3().setFromObject(model); box.getCenter(center); size = Math.max(...box.getSize(new THREE.Vector3()).toArray()); reset();
    let meshes = 0, triangles = 0, textures = new Set();
    model.traverse(o => { if (o.isMesh) { meshes++; triangles += (o.geometry.index?.count ?? o.geometry.attributes.position.count) / 3; for (const value of Object.values(o.material)) if (value?.isTexture) textures.add(value); } });
    status.textContent = `aura-${product}.glb · ${meshes} meshes · ${triangles.toLocaleString("en-US")} triangles · ${textures.size} texture bindings · loaded`;
    document.querySelectorAll("[data-product]").forEach(b => b.setAttribute("aria-pressed", String(b.dataset.product === product)));
  } catch (error) { status.textContent = `Import failed: ${error.message}`; console.error(error); }
  finally { loading = false; }
}
document.querySelectorAll("[data-product]").forEach(b => b.addEventListener("click", () => load(b.dataset.product)));
document.querySelector("#rotate").addEventListener("click", () => { camera.position.sub(controls.target).applyAxisAngle(new THREE.Vector3(0, 1, 0), Math.PI / 4).add(controls.target); controls.update(); });
document.querySelector("#zoom").addEventListener("click", () => { camera.position.sub(controls.target).multiplyScalar(.78).add(controls.target); controls.update(); });
document.querySelector("#reset").addEventListener("click", reset);
new ResizeObserver(() => { const { width, height } = canvas.getBoundingClientRect(); renderer.setSize(width, height, false); camera.aspect = width / height; camera.updateProjectionMatrix(); }).observe(canvas.parentElement);
renderer.setAnimationLoop(() => { controls.update(); renderer.render(scene, camera); });
load("studio");
