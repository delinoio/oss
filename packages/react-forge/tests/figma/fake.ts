import { CallSafety, type FigmaConnection } from "../../src/figma/transport.js";
export const FILE = "AbCdEfGhIjKlMnOpQrStUv";
export class FakeCanvas {
  private serial = 1;
  nodes = new Map<string, any>();
  fonts: string[] = [];
  calls: string[] = [];
  failProperty?: string;
  root: any;
  page: any;
  constructor() {
    this.root = this.node("DOCUMENT");
    this.page = this.node("PAGE");
    this.root.appendChild(this.page);
  }
  node(type: string) {
    const canvas = this;
    const value: any = {
      id: `1:${this.serial++}`,
      type,
      name: type,
      parent: null,
      children: [],
      x: 0,
      y: 0,
      width: 100,
      height: 100,
      fills: [],
      opacity: 1,
      visible: true,
    };
    value.appendChild = (child: any) => {
      if (child.parent) {
        child.parent.children = child.parent.children.filter(
          (c: any) => c !== child,
        );
      }
      child.parent = value;
      value.children.push(child);
    };
    value.insertChild = (index: number, child: any) => {
      value.appendChild(child);
      value.children.pop();
      value.children.splice(index, 0, child);
    };
    value.resize = (w: number, h: number) => {
      value.width = w;
      value.height = h;
    };
    value.remove = () => {
      if (value.parent)
        value.parent.children = value.parent.children.filter(
          (c: any) => c.id !== value.id,
        );
      const walk = (n: any) => {
        n.children.forEach(walk);
        this.nodes.delete(n.id);
      };
      walk(value);
    };
    value.setBoundVariable = (field: string, v: any) => {
      value.boundVariables ??= {};
      value.boundVariables[field] = { type: "VARIABLE_ALIAS", id: v.id };
    };
    value.setFillStyleIdAsync = async (id: string) => {
      value.fillStyleId = id;
    };
    value.setTextStyleIdAsync = async (id: string) => {
      value.textStyleId = id;
    };
    if (type === "TEXT") {
      value.characters = "";
      value.fontName = { family: "Inter", style: "Regular" };
      value.fontSize = 12;
      value.getStyledTextSegments = () => [{ fontName: value.fontName }];
    }
    if (type === "COMPONENT")
      value.createInstance = () => {
        const n = this.node("INSTANCE");
        n.mainComponent = value;
        n.getMainComponentAsync = async () => value;
        n.setProperties = (p: any) => {
          n.componentProperties = { ...p };
        };
        return n;
      };
    const proxy = new Proxy(value, {
      set(target, key, v) {
        if (canvas.failProperty === key) {
          canvas.failProperty = undefined;
          throw Error("fixture setter failed");
        }
        return Reflect.set(target, key, v);
      },
    });
    this.nodes.set(value.id, proxy);
    if (this.page && type !== "DOCUMENT" && type !== "PAGE")
      this.page.appendChild(proxy);
    return proxy;
  }
  get api() {
    const canvas = this;
    let current = canvas.page;
    return {
      variables: {
        createVariableCollection: (name: string) => {
          const n = canvas.node("COLLECTION");
          n.name = name;
          n.modes = [{ modeId: "m:1", name: "Mode 1" }];
          n.defaultModeId = "m:1";
          n.variableIds = [];
          return n;
        },
        createVariable: (name: string, collection: any, type: string) => {
          const n = canvas.node("VARIABLE");
          n.name = name;
          n.variableCollectionId = collection.id;
          n.resolvedType = type;
          n.valuesByMode = {};
          n.scopes = [];
          n.codeSyntax = {};
          n.setValueForMode = (mode: string, value: unknown) => {
            n.valuesByMode[mode] = value;
          };
          n.setVariableCodeSyntax = (platform: string, syntax: string) => {
            n.codeSyntax[platform] = syntax;
          };
          collection.variableIds.push(n.id);
          return n;
        },
        getVariableCollectionByIdAsync: async (id: string) =>
          canvas.nodes.get(id) ?? null,
        getVariableByIdAsync: async (id: string) =>
          canvas.nodes.get(id) ?? null,
        getLocalVariableCollectionsAsync: async () =>
          [...canvas.nodes.values()].filter((n) => n.type === "COLLECTION"),
        getLocalVariablesAsync: async () =>
          [...canvas.nodes.values()].filter((n) => n.type === "VARIABLE"),
        setBoundVariableForPaint: (
          paint: any,
          field: string,
          variable: any,
        ) => ({
          ...paint,
          boundVariables: {
            [field]: { type: "VARIABLE_ALIAS", id: variable.id },
          },
        }),
      },
      getStyleByIdAsync: async (id: string) => canvas.nodes.get(id) ?? null,
      getLocalPaintStylesAsync: async () =>
        [...canvas.nodes.values()].filter((n) => n.type === "PAINT_STYLE"),
      getLocalTextStylesAsync: async () =>
        [...canvas.nodes.values()].filter(
          (n) => n._resourceKind === "TEXT_STYLE",
        ),
      createPaintStyle: () => canvas.node("PAINT_STYLE"),
      createTextStyle: () => {
        const n = canvas.node("TEXT_STYLE");
        n._resourceKind = "TEXT_STYLE";
        n.type = "TEXT";
        return n;
      },
      root: canvas.root,
      get currentPage() {
        return current;
      },
      getNodeByIdAsync: async (id: string) => canvas.nodes.get(id) ?? null,
      setCurrentPageAsync: async (p: any) => {
        current = p;
      },
      loadFontAsync: async (f: any) => {
        canvas.fonts.push(JSON.stringify(f));
      },
      createPage: () => {
        const p = canvas.node("PAGE");
        canvas.root.appendChild(p);
        return p;
      },
      createFrame: () => canvas.node("FRAME"),
      createText: () => canvas.node("TEXT"),
      createRectangle: () => canvas.node("RECTANGLE"),
      createEllipse: () => canvas.node("ELLIPSE"),
      createLine: () => canvas.node("LINE"),
      createVector: () => canvas.node("VECTOR"),
      createComponent: () => canvas.node("COMPONENT"),
      combineAsVariants: (cs: any[], parent: any) => {
        const set = canvas.node("COMPONENT_SET");
        parent.appendChild(set);
        cs.forEach((c) => set.appendChild(c));
        return set;
      },
    };
  }
  async execute(code: string) {
    return await new Function("figma", `return (async()=>{${code}\n})();`)(
      this.api,
    );
  }
}
export class FakeConnection implements FigmaConnection {
  identity = "fixture";
  codeLimit = 50000;
  stats = { calls: 0, retries: 0, waitMs: 0 };
  closed = false;
  writes = 0;
  loseResponse = false;
  beforeWrite?: () => void;
  constructor(readonly canvas = new FakeCanvas()) {}
  async connect() {}
  async call(name: string, args: Record<string, any>, safety: CallSafety) {
    this.stats.calls++;
    this.canvas.calls.push(name);
    if (name === "whoami")
      return { plans: [{ key: "team::1", tier: "pro", seat: "Full" }] };
    if (name === "create_new_file") {
      this.writes++;
      return { file_key: FILE };
    }
    if (name === "upload_assets")
      return {
        uploads: args.nodeIds.map((id: string) => ({
          submitUrl: `https://mcp.figma.com/test-upload/${id}`,
          targetNodeId: id,
        })),
      };
    if (name === "use_figma") {
      if (safety === CallSafety.Write) {
        this.writes++;
        this.beforeWrite?.();
        this.beforeWrite = undefined;
      }
      const result = await this.canvas.execute(args.code);
      if (this.loseResponse && safety === CallSafety.Write) {
        this.loseResponse = false;
        throw Error("lost response");
      }
      if (JSON.stringify(result).length > 20000)
        throw Error("MCP result would be truncated");
      return result;
    }
    throw Error("Unexpected fixture tool");
  }
  async close() {
    this.closed = true;
  }
}
