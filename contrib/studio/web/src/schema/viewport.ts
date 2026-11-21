/** The pan and zoom of the diagram: a translation and a scale, kept in a pure module so the maths is tested. */

export interface Viewport {
  x: number;
  y: number;
  scale: number;
}

export const minScale = 0.1;
export const maxScale = 3;
export const zoomStep = 1.25;
export const panStep = 48;

export function clampScale(scale: number): number {
  return Math.min(Math.max(scale, minScale), maxScale);
}

/** fit scales the drawing down (never up past 1) to fit the box and centres it. */
export function fit(drawingW: number, drawingH: number, boxW: number, boxH: number): Viewport {
  if (drawingW <= 0 || drawingH <= 0 || boxW <= 0 || boxH <= 0) {
    return { x: 0, y: 0, scale: 1 };
  }
  const scale = clampScale(Math.min(boxW / drawingW, boxH / drawingH, 1));
  return { x: (boxW - drawingW * scale) / 2, y: (boxH - drawingH * scale) / 2, scale };
}

/** zoomBy changes the scale by a factor and keeps the point at the centre of the box where it is. */
export function zoomBy(v: Viewport, factor: number, boxW: number, boxH: number): Viewport {
  const scale = clampScale(v.scale * factor);
  if (scale === v.scale) {
    return v;
  }
  const cx = (boxW / 2 - v.x) / v.scale;
  const cy = (boxH / 2 - v.y) / v.scale;
  return { x: boxW / 2 - cx * scale, y: boxH / 2 - cy * scale, scale };
}

export function panBy(v: Viewport, dx: number, dy: number): Viewport {
  return { ...v, x: v.x + dx, y: v.y + dy };
}

/** keyAction maps a key of the diagram to a change of the viewport, or null for a key it does not own. */
export function keyAction(
  key: string,
  v: Viewport,
  boxW: number,
  boxH: number,
  drawingW: number,
  drawingH: number,
): Viewport | null {
  switch (key) {
    case "ArrowLeft":
      return panBy(v, panStep, 0);
    case "ArrowRight":
      return panBy(v, -panStep, 0);
    case "ArrowUp":
      return panBy(v, 0, panStep);
    case "ArrowDown":
      return panBy(v, 0, -panStep);
    case "+":
    case "=":
      return zoomBy(v, zoomStep, boxW, boxH);
    case "-":
    case "_":
      return zoomBy(v, 1 / zoomStep, boxW, boxH);
    case "0":
      return fit(drawingW, drawingH, boxW, boxH);
    default:
      return null;
  }
}
