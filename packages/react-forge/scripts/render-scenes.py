"""Re-import the actual Forge outputs; never repair meshes or their materials."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import sys
import bpy
from mathutils import Vector

parser = argparse.ArgumentParser()
parser.add_argument('--input', required=True)
parser.add_argument('--output', required=True)
parser.add_argument('--resolution', type=int, default=2048)
parser.add_argument('--samples', type=int, default=96)
parser.add_argument('--device', choices=['CPU', 'METAL'], default='CPU')
parser.add_argument('--products', default='studio,headphones,dac,stand')
parser.add_argument('--views', default='hero,front,back,detail')
parser.add_argument('--formats', default='glb,fbx')
args = parser.parse_args(sys.argv[sys.argv.index('--') + 1:])
source = Path(args.input).resolve()
out = Path(args.output).resolve()
out.mkdir(parents=True, exist_ok=True)
expected = {a['file']: a for a in json.loads((source / 'generation.json').read_text())['artifacts']}
report = {'blender': bpy.app.version_string, 'device': args.device, 'resolution': args.resolution, 'samples': args.samples, 'imports': []}

def aim(obj, target):
    obj.rotation_euler = (Vector(target) - obj.location).to_track_quat('-Z', 'Y').to_euler()

def area(name, location, target, energy, size, color):
    data = bpy.data.lights.new(name, 'AREA')
    data.energy, data.shape, data.size, data.color = energy, 'DISK', size, color
    obj = bpy.data.objects.new(name, data)
    bpy.context.collection.objects.link(obj)
    obj.location = location
    aim(obj, target)

for product in args.products.split(','):
    for fmt in args.formats.split(','):
        bpy.ops.wm.read_factory_settings(use_empty=True)
        name = f'aura-{product}.{fmt}'
        filepath = source / name
        if fmt == 'glb':
            bpy.ops.import_scene.gltf(filepath=str(filepath), export_import_convert_lighting_mode='SPEC')
        else:
            bpy.ops.import_scene.fbx(filepath=str(filepath), use_custom_normals=True, use_image_search=False)
        scene = bpy.context.scene
        meshes = [o for o in scene.objects if o.type == 'MESH']
        assert len(meshes) == expected[name]['meshes'], (name, 'mesh count', len(meshes))
        points = [o.matrix_world @ Vector(p) for o in meshes for p in o.bound_box]
        lo = [min(p[i] for p in points) for i in range(3)]
        hi = [max(p[i] for p in points) for i in range(3)]
        model = expected[name]['bounds']
        target_lo = [model['min'][0], -model['max'][2], model['min'][1]]
        target_hi = [model['max'][0], -model['min'][2], model['max'][1]]
        # Transformed local AABBs can be wider than exact vertex bounds. Compare
        # exact world vertices independently before adding the rendering fixture.
        verts = [o.matrix_world @ v.co for o in meshes for v in o.data.vertices]
        actual_lo = [min(v[i] for v in verts) for i in range(3)]
        actual_hi = [max(v[i] for v in verts) for i in range(3)]
        error = max(abs(a-b) for a,b in zip(actual_lo+actual_hi, target_lo+target_hi))
        assert error < 1e-5, (name, 'axis/unit/bounds', error, actual_lo, target_lo)
        images = [im for im in bpy.data.images if im.type == 'IMAGE']
        assert all(im.size[0] > 0 and im.size[1] > 0 for im in images), (name, 'missing image')
        assert all(len(o.data.materials) == 1 for o in meshes), (name, 'missing material')
        item = {'file': name, 'sha256': hashlib.sha256(filepath.read_bytes()).hexdigest(), 'meshes': len(meshes), 'triangles': sum(sum(len(p.vertices)-2 for p in o.data.polygons) for o in meshes), 'images': len(images), 'boundsErrorMeters': error, 'materials': [], 'renders': []}
        for mat in bpy.data.materials:
            bsdf = next((n for n in mat.node_tree.nodes if n.type == 'BSDF_PRINCIPLED'), None) if mat.use_nodes else None
            if bsdf:
                item['materials'].append({'name': mat.name, 'metallic': bsdf.inputs['Metallic'].default_value, 'roughness': bsdf.inputs['Roughness'].default_value, 'imageNodes': sum(n.type == 'TEX_IMAGE' for n in mat.node_tree.nodes)})
        cx,cy,cz = [(lo[i]+hi[i])/2 for i in range(3)]
        size = max(hi[i]-lo[i] for i in range(3))
        target = (cx,cy,cz)
        world = bpy.data.worlds.new('Verification studio world')
        scene.world = world
        world.use_nodes = True
        world.node_tree.nodes['Background'].inputs['Color'].default_value = (.18,.21,.25,1)
        world.node_tree.nodes['Background'].inputs['Strength'].default_value = .20
        bpy.ops.mesh.primitive_plane_add(size=size*200, location=(cx,cy,lo[2]-.001))
        ground = bpy.context.object
        ground.name = 'Verification background only'
        mat = bpy.data.materials.new('Verification warm-gray backdrop')
        mat.use_nodes = True
        mat.node_tree.nodes['Principled BSDF'].inputs['Base Color'].default_value = (.035,.043,.052,1)
        mat.node_tree.nodes['Principled BSDF'].inputs['Roughness'].default_value = .78
        ground.data.materials.append(mat)
        area('Verification key', (cx-size*.9,cy-size*1.3,cz+size*1.7), target, 100*size*size, size*1.0, (1,.92,.81))
        area('Verification fill', (cx+size*1.1,cy-size*.4,cz+size*.9), target, 55*size*size, size*.85, (.78,.89,1))
        area('Verification rim', (cx+size*.2,cy+size,cz+size*1.4), target, 180*size*size, size*.8, (1,1,1))
        camera_data = bpy.data.cameras.new('Verification camera')
        camera = bpy.data.objects.new('Verification camera', camera_data)
        scene.collection.objects.link(camera)
        scene.camera = camera
        camera_data.lens = 60
        camera_data.clip_start = .001
        scene.render.engine = 'CYCLES'
        if args.device == 'METAL':
            preferences = bpy.context.preferences.addons['cycles'].preferences
            preferences.compute_device_type = 'METAL'
            preferences.get_devices()
            assert any(d.type == 'METAL' for d in preferences.devices), 'Metal device unavailable'
            for device in preferences.devices:
                device.use = device.type == 'METAL'
            scene.cycles.device = 'GPU'
        scene.cycles.samples = args.samples
        scene.cycles.use_denoising = True
        scene.cycles.seed = 0
        scene.render.resolution_x = args.resolution
        scene.render.resolution_y = args.resolution
        scene.render.resolution_percentage = 100
        scene.render.image_settings.file_format = 'PNG'
        scene.view_settings.view_transform = 'AgX'
        for view in args.views.split(','):
            focus = target
            if view == 'hero': location=(cx+size*1.05,cy-size*1.85,cz+size*.90)
            elif view == 'front': location=(cx,cy-size*2.7,cz+size*.35)
            elif view == 'back': location=(cx-size*1.4,cy+size*2.1,cz+size*.9)
            else:
                if product == 'studio': focus=(.29,-.065,.055);location=(.51,-.53,.33)
                elif product == 'dac': focus=(.053,-.082,.04);location=(.24,-.43,.20)
                elif product == 'headphones': focus=(.085,-.004,.204);location=(.45,-.40,.36)
                else: focus=(cx,cy,hi[2]-.05);location=(cx+.23,cy-.44,hi[2]+.12)
            camera.location=location
            aim(camera,focus)
            image_name=f'aura-{product}-{fmt}-{view}.png'
            scene.render.filepath=str(out/image_name)
            bpy.ops.render.render(write_still=True)
            item['renders'].append({'file':image_name,'sha256':hashlib.sha256((out/image_name).read_bytes()).hexdigest()})
        report['imports'].append(item)
        (out/'render-report.json').write_text(json.dumps(report,indent=2)+'\n')
        print('FORGE_SCENE_VERIFIED',json.dumps({k:item[k] for k in ['file','meshes','triangles','images','boundsErrorMeters']}),flush=True)
