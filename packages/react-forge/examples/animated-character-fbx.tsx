import { Format } from "@delino/react-forge";
import { animatedCharacter } from "./animated-character.js";
export default async function run({ signal }: { signal?: AbortSignal } = {}) { return animatedCharacter(Format.Fbx, signal); }
