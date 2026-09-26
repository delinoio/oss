"""Local Blender acceptance of unmodified exported meshes, rigs and curves."""
import argparse
import hashlib
import json
import math
from pathlib import Path
import sys
import bpy
from mathutils import Vector

parser = argparse.ArgumentParser()
parser.add_argument('--input', required=True)
parser.add_argument('--render', action='store_true')
parser.add_argument('--playback', action='store_true')
args = parser.parse_args(sys.argv[sys.argv.index('--') + 1:])
root = Path(args.input)
reference = json.loads((root / 'animation-reference.json').read_text())
assert bpy.app.version_string == '4.5.14 LTS'
report = {'blender': bpy.app.version_string, 'files': []}


def select_clip(objects, name, fmt):
    count = 0
    for obj in objects:
        blocks = [obj]
        if obj.type == 'MESH' and obj.data.shape_keys:
            blocks.append(obj.data.shape_keys)
        for block in blocks:
            data = block.animation_data
            if not data:
                continue
            if fmt == 'glb':
                strips = [strip for track in data.nla_tracks if track.name == name for strip in track.strips]
                action = strips[0].action if strips else None
                slot = strips[0].action_slot if strips else None
            else:
                matches = [a for a in bpy.data.actions if a.name == f'{block.name}|{name}']
                action = matches[0] if matches else None
                slot = action.slots[0] if action else None
            data.action = action
            if action:
                data.action_slot = slot
                count += 1
    assert count >= 4, (fmt, name, 'missing animation bindings', count)


def world_bounds(meshes):
    graph = bpy.context.evaluated_depsgraph_get()
    positions = []
    for obj in meshes:
        evaluated = obj.evaluated_get(graph)
        mesh = evaluated.to_mesh()
        try:
            positions.extend(evaluated.matrix_world @ v.co for v in mesh.vertices)
        finally:
            evaluated.to_mesh_clear()
    return [min(p[i] for p in positions) for i in range(3)], [max(p[i] for p in positions) for i in range(3)]


def studio(scene):
    world = bpy.data.worlds.new('Verification environment')
    scene.world = world
    world.use_nodes = True
    world.node_tree.nodes['Background'].inputs['Color'].default_value = (.12, .16, .2, 1)
    world.node_tree.nodes['Background'].inputs['Strength'].default_value = .5
    for name, location, energy, size in [('Key', (3, -4, 6), 450, 4), ('Fill', (-3, -2, 3), 220, 3)]:
        data = bpy.data.lights.new('Verification ' + name, 'AREA')
        data.energy, data.size = energy, size
        obj = bpy.data.objects.new(data.name, data)
        scene.collection.objects.link(obj)
        obj.location = location
        obj.rotation_euler = (Vector((0, 0, 1.2)) - obj.location).to_track_quat('-Z', 'Y').to_euler()
    camera = bpy.data.objects.new('Verification camera', bpy.data.cameras.new('Verification camera'))
    scene.collection.objects.link(camera)
    camera.location = (3, -7, 3.4)
    camera.rotation_euler = (Vector((0, 0, 1.2)) - camera.location).to_track_quat('-Z', 'Y').to_euler()
    camera.data.type, camera.data.ortho_scale = 'ORTHO', 3.2
    scene.camera = camera
    scene.render.engine = 'CYCLES'
    scene.cycles.samples = 16
    scene.render.resolution_x = scene.render.resolution_y = 2048
    scene.render.resolution_percentage = 100
    scene.render.image_settings.file_format = 'PNG'


for file in reference['files']:
    bpy.ops.wm.read_factory_settings(use_empty=True)
    filepath = root / file['filename']
    assert hashlib.sha256(filepath.read_bytes()).hexdigest() == file['sha256']
    if file['format'] == 'glb':
        bpy.ops.import_scene.gltf(filepath=str(filepath), export_import_convert_lighting_mode='SPEC')
    else:
        # Explicit zero offset keeps seconds aligned across both importers.
        bpy.ops.import_scene.fbx(filepath=str(filepath), use_image_search=False, anim_offset=0)
    scene = bpy.context.scene
    objects = list(scene.objects)
    rigs = [o for o in objects if o.type == 'ARMATURE']
    custom_shapes = {b.custom_shape for rig in rigs for b in rig.pose.bones if b.custom_shape}
    meshes = [o for o in objects if o.type == 'MESH' and o not in custom_shapes]
    assert len(meshes) == 3 and sum(len(r.data.bones) for r in rigs) == 7
    assert sum(len(m.data.shape_keys.key_blocks) - 1 for m in meshes if m.data.shape_keys) == 1
    item = {'format': file['format'], 'sha256': file['sha256'], 'meshes': 3, 'joints': 7, 'morphTargets': 1, 'clips': []}
    for clip in file['clips']:
        select_clip(objects, clip['name'], file['format'])
        samples = []
        for sample in clip['samples']:
            frame = sample['time'] * scene.render.fps / scene.render.fps_base
            scene.frame_set(math.floor(frame), subframe=frame - math.floor(frame))
            lo, hi = world_bounds(meshes)
            expected_lo = [sample['min'][0], -sample['max'][2], sample['min'][1]]
            expected_hi = [sample['max'][0], -sample['min'][2], sample['max'][1]]
            error = max(abs(a-b) for a, b in zip(lo+hi, expected_lo+expected_hi))
            samples.append({'time': sample['time'], 'boundsErrorMeters': error, 'min': lo, 'max': hi})
            assert error < .002, (file['format'], clip['name'], sample['time'], error, lo, expected_lo, hi, expected_hi)
        item['clips'].append({'name': clip['name'], 'samples': samples})
    if args.render or args.playback:
        studio(scene)
    if args.render:
        item['renders'] = []
        for clip in file['clips']:
            select_clip(objects, clip['name'], file['format'])
            for time in [0, 0.25, 1, 2]:
                scene.frame_set(round(time * scene.render.fps / scene.render.fps_base))
                name = f"{file['format']}-{clip['name']}-{time}.png"
                scene.render.filepath = str(root / name)
                bpy.ops.render.render(write_still=True)
                item['renders'].append(name)
    if args.playback:
        scene.render.resolution_x = scene.render.resolution_y = 384
        scene.cycles.samples = 4
        scene.render.image_settings.file_format = 'FFMPEG'
        scene.render.ffmpeg.format = 'MPEG4'
        scene.render.ffmpeg.codec = 'H264'
        scene.frame_start, scene.frame_end = 0, round(2 * scene.render.fps / scene.render.fps_base)
        item['playback'] = []
        for clip in file['clips']:
            select_clip(objects, clip['name'], file['format'])
            name = f"{file['format']}-{clip['name']}.mp4"
            scene.render.filepath = str(root / name)
            bpy.ops.render.render(animation=True)
            item['playback'].append(name)
    report['files'].append(item)
(root / 'blender-animation.json').write_text(json.dumps(report, indent=2) + '\n')
print('FORGE_ANIMATION_VERIFIED', json.dumps({'formats': len(report['files']), 'clips': 6, 'samples': 54}))
