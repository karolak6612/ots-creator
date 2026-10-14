// WebGL2 map renderer. Tiles are drawn as instanced quads (one per sprite
// piece or colored rectangle). Each 32x32 chunk keeps its quads in its own
// GPU buffer in world pixels, built once and redrawn with a camera offset;
// only animated pieces are patched per frame. Previews and overlays are
// built every frame in screen pixels. Sprites live in a texture array atlas.
import { colorize } from "../render/outfit";
import type { SpriteCache } from "../render/sprites";
import { AF, frameAt, type Appearances, type Look } from "./appearances";
import { Atlas } from "./atlas";
import { CHUNK, IF, TF, chunkKey, splitKey, type ChunkData, type ChunkStore, type CreatureData, type TileData } from "./chunks";
import type { ItemIndex } from "./items";

export interface ViewOptions {
  grid: boolean;
  lowerFloors: boolean;
  ghostHigher: boolean;
  shade: boolean;
  creatures: boolean;
  /** Names over creatures, houses, temples and waypoints. */
  labels: boolean;
  spawns: boolean;
  houses: boolean;
  special: boolean;
  zones: boolean;
  pathing: boolean;
  animation: boolean;
  onlyModified: boolean;
  highlightItems: boolean;
  wallHooks: boolean;
  markers: boolean;
  tooltips: boolean;
  /** x, y, z written on every tile. */
  coords: boolean;
  ghostItems: boolean;
  asMinimap: boolean;
  clientBox: boolean;
  lights: boolean;
}

export interface GhostTile {
  x: number;
  y: number;
  z: number;
  /** Server ids, bottom to top. */
  ids: number[];
  subs?: number[];
  creatures?: CreatureData[];
}

export interface RectMark {
  /** Tile rectangle on a floor (inclusive). */
  x0: number;
  y0: number;
  x1: number;
  y1: number;
  z: number;
  fill?: [number, number, number, number];
  stroke?: [number, number, number, number];
}

export interface Marker {
  x: number;
  y: number;
  z: number;
  label: string;
  color: [number, number, number];
}

export interface Label {
  x: number;
  y: number;
  text: string;
  color: string;
  /** Small font, centered on y (coordinate rulers). */
  small?: boolean;
  align?: "center" | "left";
}

export interface Scene {
  width: number;
  height: number;
  /** World pixels per tile (the sprite size). */
  S: number;
  /** Camera center in world pixels (ground floor space). */
  camX: number;
  camY: number;
  /** Device pixels per world pixel. */
  scale: number;
  floor: number;
  chunks: ChunkStore;
  items: ItemIndex;
  looks: Appearances;
  sprites: SpriteCache;
  opts: ViewOptions;
  time: number;
  /** House highlighted by the house brush (0: none). */
  house: number;
  ghosts: GhostTile[];
  ghostAlpha: number;
  rects: RectMark[];
  markers: Marker[];
  /** Selected things drawn again at this offset (dragging a selection). */
  moveOffset: { dx: number; dy: number; dz: number } | null;
  minimap: MinimapLayer | null;
  /** Tile under the mouse (labelled with its position when coords is on). */
  hover: { x: number; y: number; z: number } | null;
}

export interface FrameResult {
  /** Animated things are visible: draw again soon. */
  animated: boolean;
  /** Data is still loading: draw again when it arrives. */
  pending: boolean;
  labels: Label[];
  /** The frame used the minimap instead of sprites. */
  far: boolean;
  /** Chunks are still being built: draw again on the next frame. */
  more: boolean;
}

// Quads are in "a_rect" units; a pixel is round((p + u_off) * u_scale + u_half).
// World chunks pass the camera there, screen-space quads pass (0, 1, 0).
// A negative layer hides the quad (an animation frame without a sprite).
const VERT = `#version 300 es
layout(location=0) in vec2 a_corner;
layout(location=1) in vec4 a_rect;
layout(location=2) in vec2 a_cell;
layout(location=3) in vec4 a_color;
uniform vec2 u_screen;
uniform vec2 u_off;
uniform float u_scale;
uniform vec2 u_half;
uniform float u_alpha;
uniform float u_perRow;
uniform float u_uvCell;
out vec3 v_uv;
out vec4 v_color;
void main() {
  if (a_cell.x < 0.0) {
    gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
    v_uv = vec3(0.0);
    v_color = vec4(0.0);
    return;
  }
  vec2 w = a_rect.xy + a_corner * a_rect.zw;
  vec2 p = floor((w + u_off) * u_scale + u_half + 0.5);
  gl_Position = vec4(p.x / u_screen.x * 2.0 - 1.0, 1.0 - p.y / u_screen.y * 2.0, 0.0, 1.0);
  float layer = a_cell.x;
  float col = mod(a_cell.y, u_perRow);
  float row = floor(a_cell.y / u_perRow);
  v_uv = vec3((vec2(col, row) + a_corner) * u_uvCell, layer);
  v_color = vec4(a_color.rgb, a_color.a * u_alpha);
}`;

const FRAG = `#version 300 es
precision mediump float;
precision mediump sampler2DArray;
uniform sampler2DArray u_tex;
in vec3 v_uv;
in vec4 v_color;
out vec4 outColor;
void main() {
  vec4 t = texture(u_tex, v_uv);
  if (t.a == 0.0) discard;
  outColor = t * v_color;
}`;

const VERT2 = `#version 300 es
layout(location=0) in vec2 a_corner;
uniform vec4 u_rect;
uniform vec2 u_screen;
out vec2 v_uv;
void main() {
  vec2 p = u_rect.xy + a_corner * u_rect.zw;
  gl_Position = vec4(p.x / u_screen.x * 2.0 - 1.0, 1.0 - p.y / u_screen.y * 2.0, 0.0, 1.0);
  v_uv = a_corner;
}`;

const FRAG2 = `#version 300 es
precision mediump float;
uniform sampler2D u_tex;
in vec2 v_uv;
out vec4 outColor;
void main() { outColor = texture(u_tex, v_uv); }`;

const FLOATS = 10;
/** Time spent building chunk buffers per frame; the rest wait for the next frames. */
const BUILD_MS = 8;
/** Chunk buffers kept before the least recently drawn are freed. */
const MAX_GEOS = 3000;

type DrawItem = (id: number, sub: number, x: number, y: number, z: number, ax: number, ay: number, r: number, g: number, b: number, a: number, hook: number) => number;
type DrawCreature = (c: CreatureData, ax: number, ay: number, x: number, y: number, z: number, alpha: number) => void;

/** The quads of one chunk on the GPU. */
interface ChunkGeo {
  vao: WebGLVertexArrayObject;
  buf: WebGLBuffer;
  n: number;
  /** What the quads were built for; another value means rebuild. */
  sig: string;
  atlas: Atlas;
  epoch: number;
  items: ItemIndex;
  looksRev: number;
  /** Sprites or appearances were missing: rebuild once they arrive. */
  incomplete: boolean;
  /** Atlas cells the quads use, kept alive in the atlas while drawn. */
  cells: Int32Array;
  used: number;
  /** Animated pieces: quad index, frame 0 sprite index and look. The quad data stays on the CPU to patch them. */
  data: Float32Array | null;
  animI: number[];
  animBase: number[];
  animLook: Look[];
}

interface Recorder {
  cells: Set<number>;
  animI: number[];
  animBase: number[];
  animLook: Look[];
}

function compile(gl: WebGL2RenderingContext, vs: string, fs: string): WebGLProgram {
  const p = gl.createProgram()!;
  for (const [type, src] of [
    [gl.VERTEX_SHADER, vs],
    [gl.FRAGMENT_SHADER, fs],
  ] as const) {
    const sh = gl.createShader(type)!;
    gl.shaderSource(sh, src);
    gl.compileShader(sh);
    if (!gl.getShaderParameter(sh, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(sh) ?? "shader error");
    gl.attachShader(p, sh);
  }
  gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(p) ?? "link error");
  return p;
}

/** Minimap textures for far zoom levels: one per 256x256 tile block. */
export class MinimapLayer {
  static BLOCK = 256;
  private tex = new Map<string, { t: WebGLTexture | null; rev: number; loading: boolean }>();
  private dirty = new Set<string>();
  onload: () => void = () => {};

  constructor(
    private gl: WebGL2RenderingContext,
    private url: (x: number, y: number, z: number, w: number, h: number) => string,
  ) {}

  private key(bx: number, by: number, z: number): string {
    return `${bx},${by},${z}`;
  }

  /** Marks blocks holding changed chunks (keys from chunks.ts) stale. */
  invalidate(chunkKeys: number[] | null): void {
    if (chunkKeys === null) {
      for (const k of this.tex.keys()) this.dirty.add(k);
      return;
    }
    for (const k of chunkKeys) {
      const cx = Math.floor(k / 32768);
      const cy = Math.floor(k / 16) & 0x7ff;
      const z = k & 15;
      this.dirty.add(this.key(cx >> 3, cy >> 3, z));
    }
  }

  get(bx: number, by: number, z: number): WebGLTexture | null {
    const k = this.key(bx, by, z);
    let e = this.tex.get(k);
    if (!e || (this.dirty.has(k) && !e.loading)) {
      this.dirty.delete(k);
      const entry = e ?? { t: null, rev: 0, loading: false };
      this.tex.set(k, entry);
      entry.loading = true;
      const img = new Image();
      img.onload = () => {
        const gl = this.gl;
        if (!entry.t) entry.t = gl.createTexture();
        gl.bindTexture(gl.TEXTURE_2D, entry.t);
        gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, img);
        gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST);
        gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST);
        gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
        gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
        entry.loading = false;
        this.onload();
      };
      img.onerror = () => (entry.loading = false);
      const B = MinimapLayer.BLOCK;
      img.src = this.url(bx * B, by * B, z, B, B) + `&n=${Date.now()}`;
      e = entry;
    }
    return e.t;
  }

  dispose(): void {
    for (const e of this.tex.values()) if (e.t) this.gl.deleteTexture(e.t);
    this.tex.clear();
  }
}

export class MapRenderer {
  readonly gl: WebGL2RenderingContext;
  private prog: WebGLProgram;
  private prog2: WebGLProgram;
  private vao: WebGLVertexArrayObject;
  private vao2: WebGLVertexArrayObject;
  private inst: WebGLBuffer;
  private quad: WebGLBuffer;
  private data = new Float32Array(FLOATS * 4096);
  private n = 0;
  /** Scratch buffer chunk builds write into. */
  private buildData = new Float32Array(FLOATS * 4096);
  atlas: Atlas | null = null;
  private u: Record<string, WebGLUniformLocation | null> = {};
  private u2: Record<string, WebGLUniformLocation | null> = {};
  private missing = new Set<number>();
  /** Sprite pieces that could not be placed in the atlas (counted to spot incomplete builds). */
  private misses = 0;
  private composed = new Map<string, Uint8ClampedArray | null>();
  private geos = new Map<ChunkData, ChunkGeo>();
  private rec: Recorder | null = null;
  private frameNo = 0;
  private deadline = 0;
  private built = 0;

  constructor(readonly canvas: HTMLCanvasElement) {
    const gl = canvas.getContext("webgl2", { alpha: false, antialias: false, premultipliedAlpha: false, preserveDrawingBuffer: true });
    if (!gl) throw new Error("WebGL 2 is not available");
    this.gl = gl;
    this.prog = compile(gl, VERT, FRAG);
    this.prog2 = compile(gl, VERT2, FRAG2);
    for (const k of ["u_screen", "u_off", "u_scale", "u_half", "u_alpha", "u_perRow", "u_uvCell", "u_tex"]) this.u[k] = gl.getUniformLocation(this.prog, k);
    for (const k of ["u_screen", "u_rect", "u_tex"]) this.u2[k] = gl.getUniformLocation(this.prog2, k);
    const quad = gl.createBuffer()!;
    this.quad = quad;
    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([0, 0, 1, 0, 0, 1, 1, 1]), gl.STATIC_DRAW);
    this.inst = gl.createBuffer()!;
    this.vao = this.instanceVao(this.inst);
    this.vao2 = gl.createVertexArray()!;
    gl.bindVertexArray(this.vao2);
    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.enableVertexAttribArray(0);
    gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);
    gl.bindVertexArray(null);
  }

  /** A vertex array reading quads from an instance buffer. */
  private instanceVao(buf: WebGLBuffer): WebGLVertexArrayObject {
    const gl = this.gl;
    const vao = gl.createVertexArray()!;
    gl.bindVertexArray(vao);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quad);
    gl.enableVertexAttribArray(0);
    gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);
    gl.bindBuffer(gl.ARRAY_BUFFER, buf);
    const stride = FLOATS * 4;
    gl.enableVertexAttribArray(1);
    gl.vertexAttribPointer(1, 4, gl.FLOAT, false, stride, 0);
    gl.vertexAttribDivisor(1, 1);
    gl.enableVertexAttribArray(2);
    gl.vertexAttribPointer(2, 2, gl.FLOAT, false, stride, 16);
    gl.vertexAttribDivisor(2, 1);
    gl.enableVertexAttribArray(3);
    gl.vertexAttribPointer(3, 4, gl.FLOAT, false, stride, 24);
    gl.vertexAttribDivisor(3, 1);
    gl.bindVertexArray(null);
    return vao;
  }

  /** Sprites changed in the client: drop them from the atlas (chunk buffers follow the atlas epoch). */
  invalidateSprites(ids: number[] | null): void {
    this.atlas?.invalidate(ids);
    this.composed.clear();
  }

  private freeGeo(g: ChunkGeo): void {
    this.gl.deleteVertexArray(g.vao);
    this.gl.deleteBuffer(g.buf);
  }

  dispose(): void {
    for (const g of this.geos.values()) this.freeGeo(g);
    this.geos.clear();
    this.atlas?.dispose();
    const ext = this.gl.getExtension("WEBGL_lose_context");
    ext?.loseContext();
  }

  private push(x: number, y: number, w: number, h: number, cell: number, r: number, g: number, b: number, a: number): void {
    if ((this.n + 1) * FLOATS > this.data.length) {
      const d = new Float32Array(this.data.length * 2);
      d.set(this.data);
      this.data = d;
    }
    const o = this.n * FLOATS;
    const atlas = this.atlas!;
    const d = this.data;
    d[o] = x;
    d[o + 1] = y;
    d[o + 2] = w;
    d[o + 3] = h;
    if (cell < 0) {
      d[o + 4] = -1;
      d[o + 5] = -1;
    } else {
      d[o + 4] = Math.floor(cell / atlas.perLayer);
      d[o + 5] = cell % atlas.perLayer;
      this.rec?.cells.add(cell);
    }
    d[o + 6] = r;
    d[o + 7] = g;
    d[o + 8] = b;
    d[o + 9] = a;
    this.n++;
  }

  /** A colored rectangle (screen pixels, or world pixels while building a chunk). */
  private rect(x: number, y: number, w: number, h: number, c: readonly number[]): void {
    this.push(x, y, w, h, 0, c[0], c[1], c[2], c[3]);
  }

  render(sc: Scene): FrameResult {
    const gl = this.gl;
    const result: FrameResult = { animated: false, pending: false, labels: [], far: false, more: false };
    if (!this.atlas || this.atlas.cell !== sc.S) {
      this.atlas?.dispose();
      this.atlas = new Atlas(gl, sc.S);
    }
    const atlas = this.atlas;
    atlas.begin();
    this.frameNo++;
    this.deadline = performance.now() + BUILD_MS;
    this.built = 0;
    this.n = 0;
    this.missing.clear();
    gl.viewport(0, 0, sc.width, sc.height);
    gl.clearColor(0, 0, 0, 1);
    gl.clear(gl.COLOR_BUFFER_BIT);
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);

    const { S, scale, floor } = sc;
    const T = S * scale; // device pixels per tile
    const halfW = sc.width / 2;
    const halfH = sc.height / 2;
    const adj = (z: number) => (floor <= 7 ? 7 - z : floor - z);
    const toX = (wx: number) => Math.round((wx - sc.camX) * scale + halfW);
    const toY = (wy: number) => Math.round((wy - sc.camY) * scale + halfH);
    const tileX = (x: number, z: number) => toX((x - adj(z)) * S);
    const tileY = (y: number, z: number) => toY((y - adj(z)) * S);

    let zs: number[] = [];
    if (floor <= 7) for (let z = sc.opts.lowerFloors ? 7 : floor; z >= floor; z--) zs.push(z);
    else for (let z = sc.opts.lowerFloors ? Math.min(15, floor + 2) : floor; z >= floor; z--) zs.push(z);
    const visibleTiles = ((sc.width / T + 6) * (sc.height / T + 6)) | 0;
    if (zs.length * visibleTiles > 160_000) zs = [floor, ...zs.filter((z) => z !== floor)].slice(0, Math.max(1, Math.floor(160_000 / visibleTiles))).sort((a, b) => b - a);
    const far = sc.opts.asMinimap || T < 4 || visibleTiles > 260_000;
    result.far = far;

    // Everything chunk quads depend on besides the chunk itself.
    const sigBase = `${JSON.stringify(sc.opts)}|${sc.house}`;

    if (far && sc.minimap) {
      this.drawMinimap(sc, tileX, tileY, T);
      // Overlays only.
      this.overlays(sc, result, tileX, tileY, T);
      this.flushScreen(sc);
      if (!sc.opts.asMinimap) this.prefetch(sc, sigBase, result);
      return result;
    }

    const { drawItem, drawCreature } = this.drawers(sc, scale, T, result);
    // Chunks of the current floor still loading or not built yet.
    let notReady = 0;
    const chunksOf = (z: number): ChunkData[] => {
      const a = adj(z);
      const tx0 = Math.floor((sc.camX - halfW / scale) / S + a) - 1;
      const ty0 = Math.floor((sc.camY - halfH / scale) / S + a) - 1;
      // Sprites reach up and left of their tile: take the chunks right and below too.
      const tx1 = Math.ceil((sc.camX + halfW / scale) / S + a) + 3;
      const ty1 = Math.ceil((sc.camY + halfH / scale) / S + a) + 3;
      const out: ChunkData[] = [];
      if (tx1 < 0 || ty1 < 0) return out;
      // Row by row: a sprite only overlaps tiles up and left of it, which
      // this order has already drawn, as a tile by tile walk would.
      for (let cy = Math.max(0, ty0) >> 5; cy <= ty1 >> 5; cy++) {
        for (let cx = Math.max(0, tx0) >> 5; cx <= tx1 >> 5; cx++) {
          const c = sc.chunks.get(chunkKey(cx, cy, z));
          if (c) out.push(c);
          else if (c === undefined && z === floor) notReady++;
        }
      }
      return out;
    };
    const lists = new Map<number, ChunkData[]>();
    for (const z of zs) lists.set(z, chunksOf(z));
    const ghostZ = sc.opts.ghostHigher && floor > 0 && floor !== 8 ? floor - 1 : -1;
    if (ghostZ >= 0) lists.set(ghostZ, chunksOf(ghostZ));
    // The current floor gets the build time first.
    for (const c of lists.get(floor) ?? []) if (!this.geoOf(sc, c, floor, true, sigBase, result)) notReady++;
    // Right after zooming in, parts of the view are not ready: the minimap
    // shows under them instead of black.
    if (notReady && sc.minimap) this.drawMinimap(sc, tileX, tileY, T);

    // Draw floors from the bottom up.
    for (const z of zs) {
      const isCur = z === floor;
      if (isCur && zs.length > 1 && sc.opts.shade) {
        this.rect(0, 0, sc.width, sc.height, [0, 0, 0, 0.45]);
        this.flushScreen(sc);
      }
      for (const c of lists.get(z)!) this.drawGeo(sc, c, z, isCur, 1, sigBase, adj(z), result);
      if (isCur && sc.moveOffset) {
        const a = adj(z);
        const tx0 = Math.floor((sc.camX - halfW / scale) / S + a) - 1;
        const ty0 = Math.floor((sc.camY - halfH / scale) / S + a) - 1;
        const tx1 = Math.ceil((sc.camX + halfW / scale) / S + a) + 3;
        const ty1 = Math.ceil((sc.camY + halfH / scale) / S + a) + 3;
        this.drawMoved(sc, z, tx0, ty0, tx1, ty1, tileX, tileY, T, drawItem, drawCreature);
        this.flushScreen(sc);
      }
    }
    // Ghost of the floor above.
    if (ghostZ >= 0) for (const c of lists.get(ghostZ)!) this.drawGeo(sc, c, ghostZ, false, 0.3, sigBase, adj(ghostZ), result);
    // Brush preview and paste ghost.
    for (const g of sc.ghosts) {
      if (g.z !== floor) continue;
      const ax = tileX(g.x, g.z);
      const ay = tileY(g.y, g.z);
      let elev = 0;
      // Client stacking: ground, borders, bottom, common, top items.
      const rank = (id: number) => (sc.items.is(id, "ground") ? 0 : ([3, 1, 2, 4][sc.items.top[id]] ?? 3));
      const order = g.ids.map((_, k) => k).sort((a, b) => rank(g.ids[a]) - rank(g.ids[b]) || a - b);
      for (const i of order) {
        elev += drawItem(g.ids[i], g.subs?.[i] ?? 1, g.x, g.y, g.z, ax - elev * scale, ay - elev * scale, 1, 1, 1, sc.ghostAlpha, 0);
      }
      for (const c of g.creatures ?? []) drawCreature(c, ax - elev * scale, ay - elev * scale, g.x, g.y, g.z, sc.ghostAlpha);
    }
    this.overlays(sc, result, tileX, tileY, T);
    if (this.missing.size) {
      result.pending = true;
      void sc.sprites.load([...this.missing]);
    }
    if (atlas.pending) result.pending = true;
    this.flushScreen(sc);
    this.sweep();
    return result;
  }

  /** Smallest tile size in device pixels that still draws sprites (below it the minimap shows). */
  static readonly NEAR_T = 4;

  /**
   * While the minimap shows (zoomed far out), loads and builds the chunks of
   * the current floor that zooming in to the nearest sprite zoom would show,
   * from the center out, so the first close frame is not black.
   */
  private prefetch(sc: Scene, sigBase: string, result: FrameResult): void {
    const { S, floor } = sc;
    const T = MapRenderer.NEAR_T;
    const a = floor <= 7 ? 7 - floor : 0;
    const cx = sc.camX / S + a;
    const cy = sc.camY / S + a;
    const rx = sc.width / 2 / T + 4;
    const ry = sc.height / 2 / T + 4;
    const x0 = Math.max(0, Math.floor(cx - rx)) >> 5;
    const x1 = Math.max(0, Math.ceil(cx + rx)) >> 5;
    const y0 = Math.max(0, Math.floor(cy - ry)) >> 5;
    const y1 = Math.max(0, Math.ceil(cy + ry)) >> 5;
    const ccx = cx / CHUNK;
    const ccy = cy / CHUNK;
    const keys: [number, number, number][] = [];
    for (let y = y0; y <= y1; y++) for (let x = x0; x <= x1; x++) keys.push([x, y, (x + 0.5 - ccx) ** 2 + (y + 0.5 - ccy) ** 2]);
    keys.sort((p, q) => p[2] - q[2]);
    // Building only uses the frame's leftover time; no time drawn for it is wasted.
    const scratch: FrameResult = { animated: false, pending: false, labels: [], far: true, more: false };
    for (const [x, y] of keys) {
      const c = sc.chunks.get(chunkKey(x, y, floor));
      if (!c) continue;
      const g = this.geos.get(c);
      if (g) g.used = this.frameNo;
      if (performance.now() >= this.deadline) {
        scratch.more = true;
        break;
      }
      this.geoOf(sc, c, floor, true, sigBase, scratch);
      if (g) this.atlas!.touch(g.cells);
    }
    // Keep going on the next frames until the area is ready; not when only
    // sprites are loading (they come back through their own redraw).
    if (scratch.more) result.more = true;
    else if (scratch.pending) result.pending = true;
    if (this.missing.size) void sc.sprites.load([...this.missing]);
  }

  /** The chunk's quads, built (again) when stale and the frame has build time left. */
  private geoOf(sc: Scene, chunk: ChunkData, z: number, isCur: boolean, sigBase: string, result: FrameResult): ChunkGeo | null {
    const atlas = this.atlas!;
    const sig = `${sigBase}|${isCur ? 1 : 0}`;
    let g = this.geos.get(chunk) ?? null;
    const usable = !!g && g.atlas === atlas && g.epoch === atlas.epoch;
    const fresh = usable && g!.sig === sig && g!.items === sc.items && g!.looksRev === sc.looks.rev && !g!.incomplete;
    if (!fresh) {
      if (this.built === 0 || performance.now() < this.deadline) {
        g = this.build(sc, chunk, z, isCur, sig, g);
        this.built++;
      } else {
        // Out of time: draw what there is (cells are still right) and finish next frame.
        result.more = true;
        if (!usable) return null;
      }
    }
    if (g!.incomplete) result.pending = true;
    return g;
  }

  private drawGeo(sc: Scene, chunk: ChunkData, z: number, isCur: boolean, alpha: number, sigBase: string, a: number, result: FrameResult): void {
    const g = this.geoOf(sc, chunk, z, isCur, sigBase, result);
    if (!g || !g.n) return;
    const atlas = this.atlas!;
    g.used = this.frameNo;
    atlas.touch(g.cells);
    if (g.animI.length && sc.opts.animation) {
      result.animated = true;
      this.animate(sc, g);
    }
    const gl = this.gl;
    const [cx, cy] = splitKey(chunk.key);
    this.useSprites(sc);
    gl.uniform2f(this.u.u_off, (cx * CHUNK - a) * sc.S - sc.camX, (cy * CHUNK - a) * sc.S - sc.camY);
    gl.uniform1f(this.u.u_scale, sc.scale);
    gl.uniform2f(this.u.u_half, sc.width / 2, sc.height / 2);
    gl.uniform1f(this.u.u_alpha, alpha);
    gl.bindVertexArray(g.vao);
    gl.drawArraysInstanced(gl.TRIANGLE_STRIP, 0, 4, g.n);
    gl.bindVertexArray(null);
  }

  /** Builds the quads of one chunk in world pixels relative to its corner. */
  private build(sc: Scene, chunk: ChunkData, z: number, isCur: boolean, sig: string, old: ChunkGeo | null): ChunkGeo {
    const gl = this.gl;
    const atlas = this.atlas!;
    const S = sc.S;
    const [cx, cy] = splitKey(chunk.key);
    const ox = cx * CHUNK;
    const oy = cy * CHUNK;
    const savedData = this.data;
    const savedN = this.n;
    this.data = this.buildData;
    this.n = 0;
    const misses = this.misses;
    const rec: Recorder = { cells: new Set(), animI: [], animBase: [], animLook: [] };
    this.rec = rec;
    const st = { pending: false, animated: false };
    const { drawItem, drawCreature } = this.drawers(sc, 1, S, st);
    try {
      this.drawChunk(sc, chunk, z, isCur, (x) => (x - ox) * S, (y) => (y - oy) * S, S, 1, drawItem, drawCreature);
    } finally {
      this.rec = null;
    }
    const n = this.n;
    const quads = this.data.subarray(0, n * FLOATS);
    this.buildData = this.data;
    this.data = savedData;
    this.n = savedN;

    const g: ChunkGeo = old ?? (() => {
      const buf = gl.createBuffer()!;
      return { vao: this.instanceVao(buf), buf, n: 0, sig: "", atlas, epoch: 0, items: sc.items, looksRev: 0, incomplete: false, cells: new Int32Array(0), used: 0, data: null, animI: [], animBase: [], animLook: [] };
    })();
    const animated = rec.animI.length > 0;
    gl.bindBuffer(gl.ARRAY_BUFFER, g.buf);
    gl.bufferData(gl.ARRAY_BUFFER, quads, animated ? gl.DYNAMIC_DRAW : gl.STATIC_DRAW);
    g.n = n;
    g.sig = sig;
    g.atlas = atlas;
    g.epoch = atlas.epoch;
    g.items = sc.items;
    g.looksRev = sc.looks.rev;
    g.incomplete = st.pending || this.misses !== misses;
    g.cells = Int32Array.from(rec.cells);
    g.used = this.frameNo;
    g.data = animated ? quads.slice() : null;
    g.animI = rec.animI;
    g.animBase = rec.animBase;
    g.animLook = rec.animLook;
    this.geos.set(chunk, g);
    return g;
  }

  /** Points animated pieces of a chunk at their current frame. */
  private animate(sc: Scene, g: ChunkGeo): void {
    const atlas = this.atlas!;
    const d = g.data!;
    const per = atlas.perLayer;
    let dirty = false;
    let last: Look | null = null;
    let frameOff = 0;
    for (let k = 0; k < g.animI.length; k++) {
      const look = g.animLook[k];
      if (look !== last) {
        last = look;
        frameOff = frameAt(look, sc.time) * look.pz * look.py * look.px * look.l * look.h * look.w;
      }
      const sid = look.s[g.animBase[k] + frameOff];
      let layer = -1;
      let col = -1;
      if (sid) {
        const cell = atlas.cellOf(sid, () => sc.sprites.get(sid)?.pixels);
        if (cell < 0) {
          if (!sc.sprites.get(sid)) this.missing.add(sid);
          continue;
        }
        layer = Math.floor(cell / per);
        col = cell % per;
      }
      const o = g.animI[k] * FLOATS;
      if (d[o + 4] !== layer || d[o + 5] !== col) {
        d[o + 4] = layer;
        d[o + 5] = col;
        dirty = true;
      }
    }
    if (dirty) {
      const gl = this.gl;
      gl.bindBuffer(gl.ARRAY_BUFFER, g.buf);
      gl.bufferSubData(gl.ARRAY_BUFFER, 0, d);
    }
  }

  /** Frees chunk buffers not drawn for a while once there are many. */
  private sweep(): void {
    if (this.geos.size <= MAX_GEOS) return;
    const old = this.frameNo - 60;
    for (const [c, g] of this.geos) {
      if (g.used < old) {
        this.freeGeo(g);
        this.geos.delete(c);
      }
    }
  }

  /** Item and creature drawing at a scale: screen pixels (scale, T) or world pixels (1, S). */
  private drawers(sc: Scene, scale: number, T: number, st: { pending: boolean; animated: boolean }): { drawItem: DrawItem; drawCreature: DrawCreature } {
    const items = sc.items;
    const bits = items.bits;
    const metaBit = bits.meta ?? 0;
    const fluidBits = (bits.splash ?? 0) | (bits.fluidContainer ?? 0);
    const stackBit = bits.stackable ?? 0;
    const time = sc.opts.animation ? sc.time : 0;

    const drawItem: DrawItem = (id, sub, x, y, z, ax, ay, cr, cg, cb, ca, hook) => {
      if (!items.has(id)) {
        this.rect(ax, ay, T, T, [0.8, 0, 0, 0.6 * ca]);
        return 0;
      }
      const fl = items.flags[id];
      if (fl & metaBit) return 0;
      const look = sc.looks.items.get(items.client[id]);
      if (look === undefined) {
        st.pending = true;
        return 0;
      }
      if (look === null) {
        this.rect(ax, ay, T, T, [0.8, 0, 0, 0.5 * ca]);
        return 0;
      }
      let px = 0;
      let py = 0;
      if (fl & fluidBits) {
        const c = sub % 8;
        px = (c % 4) % look.px;
        py = Math.floor(c / 4) % look.py;
      } else if (look.fl & AF.hangable && hook) {
        px = Math.min(hook, look.px - 1);
      } else if (fl & stackBit && look.px === 4 && look.py === 2) {
        const n = sub;
        if (n <= 0) [px, py] = [0, 0];
        else if (n < 5) [px, py] = [n - 1, 0];
        else if (n < 10) [px, py] = [0, 1];
        else if (n < 25) [px, py] = [1, 1];
        else if (n < 50) [px, py] = [2, 1];
        else [px, py] = [3, 1];
      } else {
        px = x % look.px;
        py = y % look.py;
      }
      const pz = z % look.pz;
      let frame = 0;
      if (look.f > 1) {
        st.animated = sc.opts.animation || st.animated;
        frame = frameAt(look, time);
      }
      this.drawLook(sc, look, px, py, pz, frame, ax - look.ox * scale, ay - look.oy * scale, T, cr, cg, cb, ca, null, sc.opts.animation);
      return look.el;
    };

    const drawCreature: DrawCreature = (c, ax, ay, x, y, z, alpha) => {
      const dark = c.selected ? 0.5 : 1;
      if (c.lookItem) {
        drawItem(c.lookItem, 0, x, y, z, ax, ay, dark, dark, dark, alpha, 0);
        return;
      }
      if (!c.lookType) {
        // Unknown creature: a marker so it is not invisible.
        this.rect(ax + T * 0.25, ay + T * 0.25, T * 0.5, T * 0.5, c.npc ? [0.3, 0.6, 1, 0.8 * alpha] : [1, 0.5, 0.2, 0.8 * alpha]);
        return;
      }
      const look = sc.looks.outfits.get(c.lookType);
      if (look === undefined) {
        st.pending = true;
        return;
      }
      if (!look) return;
      const dir = Math.min(c.dir, look.px - 1);
      let mounted = 0;
      if (c.mount && look.pz > 1) {
        const mount = sc.looks.outfits.get(c.mount);
        if (mount === undefined) st.pending = true;
        else if (mount) {
          this.drawLook(sc, mount, Math.min(dir, mount.px - 1), 0, 0, 0, ax - mount.ox * scale, ay - mount.oy * scale, T, dark, dark, dark, alpha, null);
          mounted = 1;
        }
      }
      const colors = look.l > 1 ? c : null;
      this.drawLook(sc, look, dir, 0, mounted, 0, ax - look.ox * scale, ay - look.oy * scale, T, dark, dark, dark, alpha, colors);
      for (const [bit, yy] of [
        [1, 1],
        [2, 2],
      ]) {
        if (c.addons & bit && look.py > yy) {
          this.drawLook(sc, look, dir, yy, mounted, 0, ax - look.ox * scale, ay - look.oy * scale, T, dark, dark, dark, alpha, colors);
        }
      }
    };
    return { drawItem, drawCreature };
  }

  /** Draws the tiles of one chunk in tile order. */
  private drawChunk(
    sc: Scene,
    chunk: ChunkData,
    z: number,
    isCur: boolean,
    tileX: (x: number) => number,
    tileY: (y: number) => number,
    T: number,
    scale: number,
    drawItem: DrawItem,
    drawCreature: DrawCreature,
  ): void {
    const { opts, items } = sc;
    const tops = items.top;
    const hookSouthBit = items.bits.hookSouth ?? 0;
    const hookEastBit = items.bits.hookEast ?? 0;
    const pickBit = items.bits.pickupable ?? 0;
    const [cx, cy] = splitKey(chunk.key);
    for (let idx = 0; idx < CHUNK * CHUNK; idx++) {
      const t = chunk.tiles[idx];
      if (!t) continue;
      if (opts.onlyModified && !(t.flags & TF.modified)) continue;
      const tx = cx * CHUNK + (idx & 31);
      const ty = cy * CHUNK + (idx >> 5);
      const ax0 = tileX(tx);
      const ay0 = tileY(ty);
      let r = 1;
      let g = 1;
      let b = 1;
      const f = t.flags;
      if (opts.pathing && f & TF.blocking && t.ids.length) {
        g *= 2 / 3;
        b *= 2 / 3;
      }
      if (opts.houses && f & TF.house) {
        if (t.house === sc.house) r *= 0.5;
        else {
          r *= 0.5;
          g *= 0.5;
        }
      } else if (opts.special && f & TF.pz) {
        r *= 0.5;
        b *= 0.5;
      }
      if (opts.special && f & TF.pvpZone) {
        g = r / 4;
        b = (b * 2) / 3;
      }
      if (opts.special && f & TF.noLogout) b /= 2;
      if (opts.special && f & TF.noPvp) g /= 2;
      if (opts.zones && f & TF.zones) {
        r *= 0.85;
        g *= 0.7;
      }
      if (opts.spawns && f & (TF.spawn | TF.spawnNpc)) {
        g *= 0.7;
        b *= 0.7;
      }
      if (opts.highlightItems) {
        const n = t.ids.length - (f & TF.ground ? 1 : 0);
        if (n > 0) {
          const factor = [0.75, 0.6, 0.48, 0.4, 0.33][Math.min(n, 5) - 1];
          r *= factor;
          g *= factor;
        }
      }
      let hook = 0;
      for (let i = 0; i < t.ids.length; i++) {
        const fl = items.flags[t.ids[i]] ?? 0;
        if (fl & hookSouthBit) hook = 1;
        else if (fl & hookEastBit) hook = 2;
      }
      const ids = t.ids;
      const n = ids.length;
      let elev = 0;
      let i = 0;
      const sel = (k: number) => (t.iflags[k] & IF.selected ? 0.5 : 1);
      if (f & TF.ground && n) {
        const d = sel(0);
        elev += drawItem(ids[0], t.subs[0], tx, ty, z, ax0, ay0, r * d, g * d, b * d, 1, hook);
        i = 1;
      }
      // As the client stacks them, whatever the order in the file: borders
      // (1), bottom items (2), common items, creatures, top items (3).
      const tops3: number[] = [];
      for (const pass of [1, 2, 0]) {
        for (let k = i; k < n; k++) {
          const o = tops[ids[k]];
          if (o === 3) {
            if (pass === 0) tops3.push(k);
            continue;
          }
          if (o !== pass) continue;
          const d = sel(k);
          let a = 1;
          if (opts.ghostItems && o === 0 && items.flags[ids[k]] & pickBit) a *= 0.5;
          const tint = o === 1 || o === 2;
          elev += drawItem(ids[k], t.subs[k], tx, ty, z, ax0 - elev * scale, ay0 - elev * scale, (tint ? r : 1) * d, (tint ? g : 1) * d, (tint ? b : 1) * d, a, hook);
          elev = Math.min(elev, 24);
        }
      }
      if (opts.creatures && t.creatures) {
        for (const c of t.creatures) drawCreature(c, ax0 - elev * scale, ay0 - elev * scale, tx, ty, z, 1);
      }
      for (const k of tops3) {
        const d = sel(k);
        drawItem(ids[k], t.subs[k], tx, ty, z, ax0 - elev * scale, ay0 - elev * scale, d, d, d, 1, hook);
      }
      if (!n && (opts.special || opts.houses) && f & (TF.pz | TF.house)) {
        this.rect(ax0, ay0, T, T, [r, g, b, 0.3]);
      }
      if (isCur && f & TF.selected && !n && t.creatures === null && (t.spawn || t.spawnNpc)) {
        this.rect(ax0, ay0, T, T, [0.4, 0.6, 1, 0.3]);
      }
    }
  }

  /** Draws the selected things of the current floor at the drag offset. */
  private drawMoved(
    sc: Scene,
    z: number,
    tx0: number,
    ty0: number,
    tx1: number,
    ty1: number,
    tileX: (x: number, z: number) => number,
    tileY: (y: number, z: number) => number,
    T: number,
    drawItem: DrawItem,
    drawCreature: DrawCreature,
  ): void {
    const off = sc.moveOffset!;
    for (let ty = Math.max(0, ty0 - off.dy); ty <= ty1 - off.dy; ty++) {
      for (let tx = Math.max(0, tx0 - off.dx); tx <= tx1 - off.dx; tx++) {
        const t = sc.chunks.peekTile(tx, ty, z);
        if (!t || !(t.flags & TF.selected)) continue;
        const nx = tx + off.dx;
        const ny = ty + off.dy;
        const ax = tileX(nx, z);
        const ay = tileY(ny, z);
        let elev = 0;
        for (let k = 0; k < t.ids.length; k++) {
          if (!(t.iflags[k] & IF.selected)) continue;
          elev += drawItem(t.ids[k], t.subs[k], nx, ny, z, ax - elev * sc.scale, ay - elev * sc.scale, 1, 1, 1, 0.6, 0);
        }
        for (const c of t.creatures ?? []) if (c.selected) drawCreature(c, ax, ay, nx, ny, z, 0.6);
        this.rect(ax, ay, T, T, [0.4, 0.7, 1, 0.15]);
      }
    }
  }

  private drawLook(
    sc: Scene,
    look: Look,
    px: number,
    py: number,
    pz: number,
    frame: number,
    ax: number,
    ay: number,
    T: number,
    r: number,
    g: number,
    b: number,
    a: number,
    colors: CreatureData | null = null,
    animate = false,
  ): void {
    const atlas = this.atlas!;
    const sprites = sc.sprites;
    const layers = colors ? 1 : look.l;
    // In a chunk build, animated pieces are recorded so later frames can patch them.
    const rec = animate && !colors && look.f > 1 ? this.rec : null;
    const fr = frame % Math.max(look.f, 1);
    const frameSize = look.pz * look.py * look.px * look.l * look.h * look.w;
    for (let l = 0; l < layers; l++) {
      for (let cy = 0; cy < look.h; cy++) {
        for (let cx = 0; cx < look.w; cx++) {
          const idx = (((((fr * look.pz + pz) * look.py + py) * look.px + px) * look.l + l) * look.h + cy) * look.w + cx;
          const sid = look.s[idx];
          if (rec) {
            rec.animI.push(this.n);
            rec.animBase.push(idx - fr * frameSize);
            rec.animLook.push(look);
          }
          if (!sid) {
            if (rec) this.push(ax - cx * T, ay - cy * T, T, T, -1, r, g, b, a);
            continue;
          }
          let cell: number;
          if (colors) {
            const tidx = idx + look.h * look.w; // layer 1 of the same texture
            const tpl = look.s[tidx] ?? 0;
            const key = `o:${sid}:${tpl}:${colors.head}.${colors.body}.${colors.legs}.${colors.feet}`;
            cell = atlas.cellOf(key, () => this.compose(sc.sprites, sid, tpl, colors));
            if (cell < 0) {
              this.misses++;
              if (!sprites.get(sid)) this.missing.add(sid);
              if (tpl && !sprites.get(tpl)) this.missing.add(tpl);
              continue;
            }
          } else {
            cell = atlas.cellOf(sid, () => sprites.get(sid)?.pixels);
            if (cell < 0) {
              this.misses++;
              if (!sprites.get(sid)) this.missing.add(sid);
              if (rec) this.push(ax - cx * T, ay - cy * T, T, T, -1, r, g, b, a);
              continue;
            }
          }
          this.push(ax - cx * T, ay - cy * T, T, T, cell, r, g, b, a);
        }
      }
    }
  }

  private compose(sprites: SpriteCache, sid: number, tpl: number, c: CreatureData): Uint8ClampedArray | undefined {
    const base = sprites.get(sid)?.pixels;
    if (!base) return undefined;
    if (!tpl) return base;
    const t = sprites.get(tpl)?.pixels;
    if (!t) return undefined;
    const out = new Uint8ClampedArray(base);
    colorize(out, t, { head: c.head, body: c.body, legs: c.legs, feet: c.feet });
    return out;
  }

  private drawMinimap(sc: Scene, tileX: (x: number, z: number) => number, tileY: (y: number, z: number) => number, T: number): void {
    // Drawn immediately with the second program; sprite instances follow.
    const gl = this.gl;
    const mm = sc.minimap!;
    const B = MinimapLayer.BLOCK;
    const z = sc.floor;
    const a = sc.floor <= 7 ? 7 - z : 0;
    const x0 = Math.floor(((sc.camX - sc.width / 2 / sc.scale) / sc.S + a) / B);
    const y0 = Math.floor(((sc.camY - sc.height / 2 / sc.scale) / sc.S + a) / B);
    const x1 = Math.floor(((sc.camX + sc.width / 2 / sc.scale) / sc.S + a) / B);
    const y1 = Math.floor(((sc.camY + sc.height / 2 / sc.scale) / sc.S + a) / B);
    gl.useProgram(this.prog2);
    gl.bindVertexArray(this.vao2);
    gl.uniform2f(this.u2.u_screen, sc.width, sc.height);
    gl.activeTexture(gl.TEXTURE1);
    gl.uniform1i(this.u2.u_tex, 1);
    for (let by = Math.max(0, y0); by <= y1; by++) {
      for (let bx = Math.max(0, x0); bx <= x1; bx++) {
        const tex = mm.get(bx, by, z);
        if (!tex) continue;
        gl.bindTexture(gl.TEXTURE_2D, tex);
        gl.uniform4f(this.u2.u_rect, tileX(bx * B, z), tileY(by * B, z), B * T, B * T);
        gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
      }
    }
    gl.activeTexture(gl.TEXTURE0);
  }

  private overlays(sc: Scene, result: FrameResult, tileX: (x: number, z: number) => number, tileY: (y: number, z: number) => number, T: number): void {
    const z = sc.floor;
    const line = Math.max(1, Math.round(sc.scale));
    const stroke = (x: number, y: number, w: number, h: number, c: readonly number[], lw = line) => {
      this.rect(x, y, w, lw, c);
      this.rect(x, y + h - lw, w, lw, c);
      this.rect(x, y, lw, h, c);
      this.rect(x + w - lw, y, lw, h, c);
    };
    if (sc.opts.grid && T >= 8) {
      const halfW = sc.width / 2;
      const halfH = sc.height / 2;
      const a = sc.floor <= 7 ? 7 - z : 0;
      const tx0 = Math.floor((sc.camX - halfW / sc.scale) / sc.S + a);
      const ty0 = Math.floor((sc.camY - halfH / sc.scale) / sc.S + a);
      const tx1 = Math.ceil((sc.camX + halfW / sc.scale) / sc.S + a);
      const ty1 = Math.ceil((sc.camY + halfH / sc.scale) / sc.S + a);
      const c = [1, 1, 1, 0.12];
      for (let x = tx0; x <= tx1; x++) this.rect(tileX(x, z), 0, 1, sc.height, c);
      for (let y = ty0; y <= ty1; y++) this.rect(0, tileY(y, z), sc.width, 1, c);
    }
    // Spawn radius outlines of the current floor.
    if (sc.opts.spawns && !result.far) {
      const halfW = sc.width / 2 / sc.scale / sc.S;
      const halfH = sc.height / 2 / sc.scale / sc.S;
      const a = sc.floor <= 7 ? 7 - z : 0;
      const cx = sc.camX / sc.S + a;
      const cy = sc.camY / sc.S + a;
      const R = 30;
      for (let ty = Math.max(0, Math.floor(cy - halfH - R)); ty <= cy + halfH + R; ty++) {
        for (let tx = Math.max(0, Math.floor(cx - halfW - R)); tx <= cx + halfW + R; tx++) {
          const t = sc.chunks.peekTile(tx, ty, z);
          if (!t || !(t.flags & (TF.spawn | TF.spawnNpc))) continue;
          for (const s of [t.spawn, t.spawnNpc]) {
            if (!s) continue;
            const r = s.radius;
            const c = s.selected ? [1, 1, 0.4, 0.9] : t.spawnNpc === s ? [0.4, 0.7, 1, 0.7] : [1, 0.45, 0.45, 0.7];
            stroke(tileX(tx - r, z), tileY(ty - r, z), (2 * r + 1) * T, (2 * r + 1) * T, c);
            this.rect(tileX(tx, z), tileY(ty, z), T, T, [c[0], c[1], c[2], 0.25]);
          }
        }
      }
    }
    for (const m of sc.rects) {
      if (m.z !== z) continue;
      const x = tileX(Math.min(m.x0, m.x1), z);
      const y = tileY(Math.min(m.y0, m.y1), z);
      const w = (Math.abs(m.x1 - m.x0) + 1) * T;
      const h = (Math.abs(m.y1 - m.y0) + 1) * T;
      if (m.fill) this.rect(x, y, w, h, m.fill);
      if (m.stroke) stroke(x, y, w, h, m.stroke);
    }
    if (sc.opts.markers) {
      for (const mk of sc.markers) {
        if (mk.z !== z) continue;
        const x = tileX(mk.x, z);
        const y = tileY(mk.y, z);
        const c = [mk.color[0], mk.color[1], mk.color[2], 0.9];
        stroke(x + line, y + line, T - 2 * line, T - 2 * line, c, Math.max(2, line * 2));
        if (T >= 12 && sc.opts.labels) {
          result.labels.push({
            x: x + T / 2,
            y: y - 2,
            text: mk.label,
            color: `rgb(${Math.round(mk.color[0] * 255)},${Math.round(mk.color[1] * 255)},${Math.round(mk.color[2] * 255)})`,
          });
        }
      }
    }
    if (sc.opts.clientBox) {
      // The client shows 15x11 tiles around the player (plus one hidden row).
      const a = sc.floor <= 7 ? 7 - z : 0;
      const cx = Math.floor(sc.camX / sc.S + a);
      const cy = Math.floor(sc.camY / sc.S + a);
      stroke(tileX(cx - 7, z), tileY(cy - 5, z), 15 * T, 11 * T, [1, 1, 1, 0.6], Math.max(2, line));
    }
    if (sc.opts.creatures && sc.opts.labels && T >= 24 && !result.far) {
      // Names over creatures as the client shows them: green (full health),
      // centered over the sprite, which is drawn 8 px up and left of its tile.
      const halfW = sc.width / 2 / sc.scale / sc.S;
      const halfH = sc.height / 2 / sc.scale / sc.S;
      const a = sc.floor <= 7 ? 7 - z : 0;
      const cx = sc.camX / sc.S + a;
      const cy = sc.camY / sc.S + a;
      const d = T / 4;
      for (let ty = Math.max(0, Math.floor(cy - halfH)); ty <= cy + halfH + 1; ty++) {
        for (let tx = Math.max(0, Math.floor(cx - halfW)); tx <= cx + halfW + 1; tx++) {
          const t = sc.chunks.peekTile(tx, ty, z);
          if (!t?.creatures) continue;
          for (const c of t.creatures) result.labels.push({ x: tileX(tx, z) + T / 2 - d, y: tileY(ty, z) - d - 2, text: c.name, color: "#00c000" });
        }
      }
    }
    if (sc.opts.coords && sc.hover && sc.hover.z === z) {
      // The position of the tile under the mouse, in a dark tag above it.
      const h = sc.hover;
      const s = Math.max(1, Math.round(globalThis.devicePixelRatio || 1));
      const text = `${h.x}, ${h.y}, ${h.z}`;
      const w = (text.length * 5 + 8) * s;
      const ht = 13 * s;
      const x = tileX(h.x, z);
      const y = tileY(h.y, z);
      const tx = Math.min(Math.max(0, x + T / 2 - w / 2), sc.width - w);
      const ty = y - ht - 2 * s >= 0 ? y - ht - 2 * s : y + T + 2 * s;
      this.rect(x, y, T, T, [1, 1, 1, 0.12]);
      this.rect(tx, ty, w, ht, [0, 0, 0, 0.75]);
      result.labels.push({ x: tx + w / 2, y: ty + ht / 2, text, color: "#e8e8e8", small: true });
    }
    if (sc.opts.tooltips && T >= 24 && !result.far) {
      const halfW = sc.width / 2 / sc.scale / sc.S;
      const halfH = sc.height / 2 / sc.scale / sc.S;
      const a = sc.floor <= 7 ? 7 - z : 0;
      const cx = sc.camX / sc.S + a;
      const cy = sc.camY / sc.S + a;
      for (let ty = Math.max(0, Math.floor(cy - halfH)); ty <= cy + halfH; ty++) {
        for (let tx = Math.max(0, Math.floor(cx - halfW)); tx <= cx + halfW; tx++) {
          const t = sc.chunks.peekTile(tx, ty, z);
          if (!t) continue;
          for (let k = 0; k < t.ids.length; k++) {
            const f = t.iflags[k];
            if (!(f & (IF.ids | IF.text | IF.teleport | IF.doorId))) continue;
            const tags: string[] = [];
            if (f & IF.ids) tags.push("id");
            if (f & IF.text) tags.push("text");
            if (f & IF.teleport) tags.push("tp");
            if (f & IF.doorId) tags.push("door");
            result.labels.push({ x: tileX(tx, z) + T / 2, y: tileY(ty, z) + T / 2, text: `${t.ids[k]} ${tags.join(" ")}`, color: "#ffd75e" });
            break;
          }
        }
      }
    }
  }

  /** Binds the sprite program and atlas (the atlas texture can change while building). */
  private useSprites(sc: Scene): void {
    const gl = this.gl;
    gl.useProgram(this.prog);
    gl.activeTexture(gl.TEXTURE0);
    gl.bindTexture(gl.TEXTURE_2D_ARRAY, this.atlas!.tex);
    gl.uniform1i(this.u.u_tex, 0);
    gl.uniform2f(this.u.u_screen, sc.width, sc.height);
    gl.uniform1f(this.u.u_perRow, this.atlas!.perRow);
    gl.uniform1f(this.u.u_uvCell, this.atlas!.uvCell);
  }

  /** Draws the screen-space quads pushed so far. */
  private flushScreen(sc: Scene): void {
    const gl = this.gl;
    if (!this.n) return;
    this.useSprites(sc);
    gl.uniform2f(this.u.u_off, 0, 0);
    gl.uniform1f(this.u.u_scale, 1);
    gl.uniform2f(this.u.u_half, 0, 0);
    gl.uniform1f(this.u.u_alpha, 1);
    gl.bindVertexArray(this.vao);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.inst);
    gl.bufferData(gl.ARRAY_BUFFER, this.data.subarray(0, this.n * FLOATS), gl.STREAM_DRAW);
    gl.drawArraysInstanced(gl.TRIANGLE_STRIP, 0, 4, this.n);
    gl.bindVertexArray(null);
    this.n = 0;
  }
}

/** Light-weight per-tile draw description shared by palettes and ghosts. */
export type { TileData };
