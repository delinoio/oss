"""Verify the camera/light/material profile and reflected nonuniform hierarchy."""
import json
from pathlib import Path
import sys
import bpy
from mathutils.kdtree import KDTree
root = Path(sys.argv[sys.argv.index('--') + 1])
records = []
for fmt in ['glb', 'fbx']:
    bpy.ops.wm.read_factory_settings(use_empty=True)
    if fmt == 'glb':
        bpy.ops.import_scene.gltf(filepath=str(root / 'profile.glb'), export_import_convert_lighting_mode='SPEC')
    else:
        bpy.ops.import_scene.fbx(filepath=str(root / 'profile.fbx'), use_image_search=False)
    scene = bpy.context.scene
    objects = {o.name: o for o in scene.objects}
    assert len(objects) == 8
    mesh = objects['Opacity probe']
    assert mesh.parent.name == 'Mirrored nonuniform parent'
    assert mesh.matrix_world.determinant() < 0
    material = mesh.data.materials[0]
    bsdf = next(n for n in material.node_tree.nodes if n.type == 'BSDF_PRINCIPLED')
    scalars = {k: bsdf.inputs[k].default_value for k in ['Metallic', 'Roughness', 'Alpha']}
    for key, expected in [('Metallic', .7), ('Roughness', .35), ('Alpha', .4)]:
        assert abs(scalars[key] - expected) < 1e-6, (fmt, key, scalars)
    assert objects['Perspective'].data.type == 'PERSP'
    assert objects['Orthographic'].data.type == 'ORTHO'
    assert abs(objects['Orthographic'].data.ortho_scale - .8) < 1e-6
    assert [objects[n].data.type for n in ['Sun', 'Point', 'Spot']] == ['SUN', 'POINT', 'SPOT']
    assert abs(objects['Spot'].data.spot_size - 1) < 1e-6
    assert abs(objects['Spot'].data.spot_blend - .6) < 1e-6
    record = {'format': fmt, 'scalars': scalars,
              # glTF bakes the up-axis conversion into mesh data; FBX applies
              # it to the root. Compare their resulting world vertices, not
              # those two equivalent intermediate matrix factorizations.
              'worldVertices': sorted([list(mesh.matrix_world @ v.co) for v in mesh.data.vertices]),
              'matrices': {n: [v for row in objects[n].matrix_world for v in row] for n in ['Perspective', 'Orthographic', 'Sun', 'Point', 'Spot']},
              'lights': {n: [objects[n].data.energy, *objects[n].data.color] for n in ['Sun', 'Point', 'Spot']},
              'cameras': {n: [objects[n].data.clip_start, objects[n].data.clip_end] for n in ['Perspective', 'Orthographic']}}
    records.append(record)
for field in ['matrices', 'lights', 'cameras']:
    for name, values in records[0][field].items():
        assert max(abs(a-b) for a,b in zip(values, records[1][field][name])) < 1e-5, (field, name)
assert len(records[0]['worldVertices']) == len(records[1]['worldVertices'])
for source, target in [(records[0], records[1]), (records[1], records[0])]:
    # Exporters/importers may reorder equivalent vertices. Bidirectional nearest
    # distance avoids lexicographic instability for nearly equal coordinates.
    tree = KDTree(len(target['worldVertices']))
    for i, vertex in enumerate(target['worldVertices']):
        tree.insert(vertex, i)
    tree.balance()
    error = max(tree.find(vertex)[2] for vertex in source['worldVertices'])
    assert error < 1e-5, ('world vertices differ', error)
    source['worldVertexComparisonErrorMeters'] = error
for record in records:
    record['worldVertexCount'] = len(record.pop('worldVertices'))
(root / 'profile-report.json').write_text(json.dumps({'blender': bpy.app.version_string, 'checks': records, 'result': 'passed'}, indent=2)+'\n')
print('FORGE_SCENE_PROFILE_VERIFIED')
