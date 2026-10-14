import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(fileURLToPath(import.meta.url));

export default {
  mode: "production",
  context: root,
  entry: { main: "./src/main.js" },
  output: {
    path: path.join(root, "dist"),
    filename: "[name].js",
    chunkFilename: "[name].chunk.js",
  },
};
