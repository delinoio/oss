"""Compare independently imported scene structure before adding render fixtures."""
import argparse
import json
import re
import struct
from pathlib import Path
import sys
import bpy

parser = argparse.ArgumentParser()
parser.add_argument('--input', required=True)
args = parser.parse_args(sys.argv[sys.argv.index('--') + 1:])
root = Path(args.input)
records = []
for product in ['studio', 'headphones', 'dac', 'stand']:
    glb_bytes = (root / f'aura-{product}.glb').read_bytes()
    document = json.loads(glb_bytes[20:20+struct.unpack_from('<I', glb_bytes, 12)[0]])
    authored_names = {n.get('name', '') for n in document['nodes']}
    pair = []
    for fmt in ['glb', 'fbx']:
        bpy.ops.wm.read_factory_settings(use_empty=True)
        file = f'aura-{product}.{fmt}'
        if fmt == 'glb':
            bpy.ops.import_scene.gltf(filepath=str(root / file), export_import_convert_lighting_mode='SPEC')
        else:
            bpy.ops.import_scene.fbx(filepath=str(root / file), use_custom_normals=True, use_image_search=False)
        objects = list(bpy.context.scene.objects)
        cameras = [o for o in objects if o.type == 'CAMERA']
        lights = [o for o in objects if o.type == 'LIGHT']
        assert len(cameras) == len(lights) == 1
        camera = cameras[0]
        assert camera.data.type == 'PERSP'
        assert abs(camera.data.clip_start - .01) < 1e-6
        assert abs(camera.data.clip_end - 100) < 1e-4
        assert abs(camera.data.angle_y - .50) < 1e-5
        assert lights[0].data.type == 'POINT'
        meshes = [o for o in objects if o.type == 'MESH']
        assert all(len(o.data.uv_layers) == 1 and len(o.data.materials) == 1 for o in meshes)
        def hierarchy(obj):
            # Blender invents different names for unnamed glTF/FBX nodes and
            # suffixes duplicate names. Compare the authored hierarchy without
            # changing either imported scene or treating invented names as data.
            name = re.sub(r'\.\d{3}$', '', obj.name)
            return [name if name in authored_names else '', obj.type,
                    sorted((hierarchy(child) for child in obj.children), key=repr)]
        record = {'file': file, 'objects': len(objects), 'hierarchy': sorted((hierarchy(o) for o in objects if not o.parent), key=repr),
                  'cameraMatrix': [v for row in camera.matrix_world for v in row],
                  'cameraYfov': camera.data.angle_y, 'lightEnergy': lights[0].data.energy,
                  'lightColor': list(lights[0].data.color), 'meshUvLayers': 1}
        pair.append(record)
        records.append(record)
    a, b = pair
    assert a['hierarchy'] == b['hierarchy'], (product, 'authored hierarchy differs')
    for key in ['cameraMatrix', 'lightColor']:
        assert max(abs(x-y) for x,y in zip(a[key], b[key])) < 1e-5, (product, key)
    assert abs(a['lightEnergy'] - b['lightEnergy']) < 1e-6
(root / 'structure-report.json').write_text(json.dumps({'blender': bpy.app.version_string, 'imports': records, 'pairedHierarchyCameraLightChecks': 'passed'}, indent=2)+'\n')
print('FORGE_SCENE_STRUCTURE_VERIFIED', len(records))
