import * as THREE from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import { OrbitControls } from "three/addons/controls/OrbitControls.js";
import { RoomEnvironment } from "three/addons/environments/RoomEnvironment.js";
const canvas=document.querySelector("canvas"), status=document.querySelector("#status"), slider=document.querySelector("#time"), label=document.querySelector("#time-label"), play=document.querySelector("#play");
const renderer=new THREE.WebGLRenderer({canvas,antialias:true}); renderer.setPixelRatio(Math.min(devicePixelRatio,2)); renderer.toneMapping=THREE.AgXToneMapping;
const scene=new THREE.Scene(); scene.background=new THREE.Color("#111c29");
const pmrem=new THREE.PMREMGenerator(renderer), room=new RoomEnvironment(); scene.environment=pmrem.fromScene(room).texture; room.dispose();pmrem.dispose();
const camera=new THREE.PerspectiveCamera(35,1,.01,100);camera.position.set(3,2.8,6);
const controls=new OrbitControls(camera,canvas);controls.target.set(0,1.2,0);controls.enableDamping=true;controls.update();
const floor=new THREE.Mesh(new THREE.PlaneGeometry(200,200),new THREE.MeshStandardMaterial({color:"#152334",roughness:.7}));floor.rotation.x=-Math.PI/2;floor.position.y=.01;scene.add(floor);
let mixer, action, model, time=0, playing=false, previous=performance.now();
function paused(){playing=false;play.textContent="Play";play.setAttribute("aria-pressed","false");}
function sample(){
  mixer.setTime(time);model.updateMatrixWorld(true);model.traverse(o=>{if(o.isSkinnedMesh)o.skeleton.update();});
  slider.value=String(time);label.textContent=`${time.toFixed(2)} s`;
  const bounds=new THREE.Box3().setFromObject(model,true);
  status.textContent=`${action.getClip().name} · ${time.toFixed(2)} s · world bounds ${bounds.min.toArray().map(n=>n.toFixed(3)).join(", ")} → ${bounds.max.toArray().map(n=>n.toFixed(3)).join(", ")}`;
}
try {
  const gltf=await new GLTFLoader().loadAsync("/animated-character.glb"); model=gltf.scene;scene.add(model);mixer=new THREE.AnimationMixer(model);
  for(const clip of gltf.animations){
    const button=document.createElement("button");button.textContent=clip.name;button.setAttribute("aria-pressed","false");
    button.addEventListener("click",()=>{paused();mixer.stopAllAction();mixer.setTime(0);action=mixer.clipAction(clip);action.setLoop(THREE.LoopOnce,1);action.clampWhenFinished=true;action.reset().play();time=0;slider.max=String(clip.duration);document.querySelectorAll("#clips button").forEach(b=>b.setAttribute("aria-pressed",String(b===button)));sample();});
    document.querySelector("#clips").append(button);
  }
  document.querySelector("#clips button").click();
} catch(error){status.textContent=`Import failed: ${error.message}`;throw error;}
slider.addEventListener("input",()=>{paused();time=Number(slider.value);action.paused=false;sample();});
play.addEventListener("click",()=>{playing=!playing;play.textContent=playing?"Pause":"Play";play.setAttribute("aria-pressed",String(playing));previous=performance.now();});
new ResizeObserver(()=>{const {width,height}=canvas.parentElement.getBoundingClientRect();renderer.setSize(width,height,false);camera.aspect=width/height;camera.updateProjectionMatrix();}).observe(canvas.parentElement);
renderer.setAnimationLoop(()=>{const now=performance.now();if(playing&&action){time=(time+(now-previous)/1000)%action.getClip().duration;action.paused=false;sample();}previous=now;controls.update();renderer.render(scene,camera);});
