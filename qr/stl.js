// stl.js — binary STL generation helpers for the QR generator.
//
// Everything works on simple 2D polygons (arrays of [x, y] points, in mm)
// extruded along Z into closed solids. Triangulation of polygon caps —
// including glyph outlines with holes — is done by the vendored earcut.
// app.js gathers the same module-unit geometry the SVG/PNG renderers use,
// maps it to millimetres, and feeds it through these helpers.

(function () {
  "use strict";

  function signedArea(pts) {
    let s = 0;
    for (let i = 0, n = pts.length; i < n; i++) {
      const [x1, y1] = pts[i];
      const [x2, y2] = pts[(i + 1) % n];
      s += x1 * y2 - x2 * y1;
    }
    return s / 2;
  }

  function ensureWinding(pts, ccw) {
    if (signedArea(pts) > 0 !== ccw) pts.reverse();
    return pts;
  }

  // Extrude a polygon (outer ring + optional hole rings) from z0 to z1 and
  // append the resulting closed solid's triangles to `out`. Winding of the
  // inputs doesn't matter — it's normalized here.
  function extrudePolygon(outer, holes, z0, z1, out) {
    if (outer.length < 3) return;
    ensureWinding(outer, true);
    for (const h of holes) ensureWinding(h, false);

    const flat = [];
    const holeIdx = [];
    for (const p of outer) flat.push(p[0], p[1]);
    for (const h of holes) {
      holeIdx.push(flat.length / 2);
      for (const p of h) flat.push(p[0], p[1]);
    }
    const idx = earcut(flat, holeIdx.length ? holeIdx : null, 2);

    const px = (i) => flat[i * 2];
    const py = (i) => flat[i * 2 + 1];
    for (let i = 0; i < idx.length; i += 3) {
      const a = idx[i], b = idx[i + 1], c = idx[i + 2];
      // Top cap (+z) keeps earcut's winding; bottom cap (−z) reverses it.
      out.push([px(a), py(a), z1, px(b), py(b), z1, px(c), py(c), z1]);
      out.push([px(a), py(a), z0, px(c), py(c), z0, px(b), py(b), z0]);
    }

    // Walls: with a CCW outer and CW holes this ordering yields outward
    // normals everywhere.
    const rings = [outer].concat(holes);
    for (const ring of rings) {
      for (let i = 0, n = ring.length; i < n; i++) {
        const [xi, yi] = ring[i];
        const [xj, yj] = ring[(i + 1) % n];
        out.push([xi, yi, z0, xj, yj, z0, xj, yj, z1]);
        out.push([xi, yi, z0, xj, yj, z1, xi, yi, z1]);
      }
    }
  }

  function ngon(cx, cy, r, n) {
    const pts = [];
    for (let i = 0; i < n; i++) {
      const a = (Math.PI * 2 * i) / n;
      pts.push([cx + r * Math.cos(a), cy + r * Math.sin(a)]);
    }
    return pts;
  }

  function rectPts(x, y, w, h) {
    return [[x, y], [x + w, y], [x + w, y + h], [x, y + h]];
  }

  function roundedRectPts(x, y, w, h, r, seg) {
    seg = seg || 6;
    r = Math.min(r, w / 2, h / 2);
    const pts = [];
    const corner = (cx, cy, a0) => {
      for (let i = 0; i <= seg; i++) {
        const a = a0 + (Math.PI / 2) * (i / seg);
        pts.push([cx + r * Math.cos(a), cy + r * Math.sin(a)]);
      }
    };
    corner(x + w - r, y + r, -Math.PI / 2);      // top-right
    corner(x + w - r, y + h - r, 0);             // bottom-right
    corner(x + r, y + h - r, Math.PI / 2);       // bottom-left
    corner(x + r, y + r, Math.PI);               // top-left
    return pts;
  }

  function hexPts(cx, cy, rr) {
    const pts = [];
    for (let i = 0; i < 6; i++) {
      const a = (Math.PI / 180) * (60 * i - 90);
      pts.push([cx + rr * Math.cos(a), cy + rr * Math.sin(a)]);
    }
    return pts;
  }

  // Flatten an opentype.js path (the commands array) into polygon contours,
  // sampling curves. Returns [{outer, holes}] — contour nesting is resolved
  // by containment depth so glyphs like O, B, % come out with proper holes.
  function glyphToPolys(commands, curveSteps) {
    const steps = curveSteps || 8;
    const contours = [];
    let cur = null;
    let sx = 0, sy = 0, cx = 0, cy = 0;

    for (const cmd of commands) {
      switch (cmd.type) {
        case "M":
          cur = [[cmd.x, cmd.y]];
          contours.push(cur);
          sx = cx = cmd.x; sy = cy = cmd.y;
          break;
        case "L":
          cur.push([cmd.x, cmd.y]);
          cx = cmd.x; cy = cmd.y;
          break;
        case "Q":
          for (let i = 1; i <= steps; i++) {
            const t = i / steps, u = 1 - t;
            cur.push([
              u * u * cx + 2 * u * t * cmd.x1 + t * t * cmd.x,
              u * u * cy + 2 * u * t * cmd.y1 + t * t * cmd.y,
            ]);
          }
          cx = cmd.x; cy = cmd.y;
          break;
        case "C":
          for (let i = 1; i <= steps; i++) {
            const t = i / steps, u = 1 - t;
            cur.push([
              u * u * u * cx + 3 * u * u * t * cmd.x1 + 3 * u * t * t * cmd.x2 + t * t * t * cmd.x,
              u * u * u * cy + 3 * u * u * t * cmd.y1 + 3 * u * t * t * cmd.y2 + t * t * t * cmd.y,
            ]);
          }
          cx = cmd.x; cy = cmd.y;
          break;
        case "Z":
          cx = sx; cy = sy;
          break;
      }
    }

    const clean = contours
      .map((c) => {
        // Drop a duplicated closing point if present.
        const n = c.length;
        if (n > 1 && c[0][0] === c[n - 1][0] && c[0][1] === c[n - 1][1]) c.pop();
        return c;
      })
      .filter((c) => c.length >= 3 && Math.abs(signedArea(c)) > 1e-6);

    const inside = (pt, poly) => {
      let odd = false;
      for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) {
        const [xi, yi] = poly[i], [xj, yj] = poly[j];
        if (yi > pt[1] !== yj > pt[1] &&
            pt[0] < ((xj - xi) * (pt[1] - yi)) / (yj - yi) + xi) {
          odd = !odd;
        }
      }
      return odd;
    };

    const info = clean.map((c) => ({ pts: c, area: Math.abs(signedArea(c)), depth: 0 }));
    for (const a of info) {
      for (const b of info) {
        if (a !== b && inside(a.pts[0], b.pts)) a.depth++;
      }
    }

    const groups = [];
    for (const c of info) {
      if (c.depth % 2 === 0) groups.push({ outer: c.pts, holes: [], area: c.area, ref: c });
    }
    for (const c of info) {
      if (c.depth % 2 === 1) {
        // Attach to the smallest even-depth contour that contains it.
        let best = null;
        for (const g of groups) {
          if (inside(c.pts[0], g.outer) && (!best || g.area < best.area)) best = g;
        }
        if (best) best.holes.push(c.pts);
      }
    }
    return groups;
  }

  // Serialize triangles ([ax,ay,az, bx,by,bz, cx,cy,cz] each) to binary STL.
  function writeBinary(tris, name) {
    const valid = [];
    const norms = [];
    for (const t of tris) {
      const ux = t[3] - t[0], uy = t[4] - t[1], uz = t[5] - t[2];
      const vx = t[6] - t[0], vy = t[7] - t[1], vz = t[8] - t[2];
      const nx = uy * vz - uz * vy;
      const ny = uz * vx - ux * vz;
      const nz = ux * vy - uy * vx;
      const len = Math.hypot(nx, ny, nz);
      if (len < 1e-12) continue; // degenerate sliver
      valid.push(t);
      norms.push([nx / len, ny / len, nz / len]);
    }

    const buf = new ArrayBuffer(84 + valid.length * 50);
    const dv = new DataView(buf);
    const header = (name || "hipposcottomus qr coaster").slice(0, 79);
    for (let i = 0; i < header.length; i++) dv.setUint8(i, header.charCodeAt(i));
    dv.setUint32(80, valid.length, true);
    let o = 84;
    for (let i = 0; i < valid.length; i++) {
      const n = norms[i], t = valid[i];
      dv.setFloat32(o, n[0], true); dv.setFloat32(o + 4, n[1], true); dv.setFloat32(o + 8, n[2], true);
      o += 12;
      for (let v = 0; v < 9; v++) { dv.setFloat32(o, t[v], true); o += 4; }
      dv.setUint16(o, 0, true); o += 2;
    }
    return buf;
  }

  window.qrStl = { extrudePolygon, ngon, rectPts, roundedRectPts, hexPts, glyphToPolys, writeBinary, signedArea };
})();
