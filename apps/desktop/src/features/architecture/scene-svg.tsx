import { polylinePath, type Rect } from "./geometry"
import { ActorShape, CardShape, DiagramDefs, EdgeShape, GroupShape, HeaderShape, LegendShape } from "./svg"
import { groupIconWidth } from "./scene"
import type { Scene } from "./use-scene"

/**
 * The whole diagram as one SVG element, drawn from the same shapes as the canvas: groups (parents
 * first), edges, cards and actors, then the title block and the legend on top.
 */
export function buildSceneSvg({
  scene,
  bounds,
  icon,
  fonts,
  background,
  title,
  legend,
}: {
  scene: Scene
  bounds: Rect
  /** Maps an asset URL to the URL to write (a data: URL in exports). */
  icon: (url: string) => string
  /** CSS @font-face rules embedded in the document. */
  fonts: string
  background?: string
  title: boolean
  legend: boolean
}) {
  const { model, diagram, deco } = scene
  const groups = new Map(model.groups.map((g) => [g.id, g]))
  const actors = new Map(model.actors.map((a) => [a.id, a]))
  const cards = new Map(model.cards.map((c) => [c.id, c]))
  const edges = diagram.edges.map((e) => ({ e, path: polylinePath(e.route) }))
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={bounds.width}
      height={bounds.height}
      viewBox={`${bounds.x} ${bounds.y} ${bounds.width} ${bounds.height}`}
    >
      <style>{fonts}</style>
      <DiagramDefs />
      {background && <rect x={bounds.x} y={bounds.y} width={bounds.width} height={bounds.height} fill={background} />}
      {diagram.items
        .filter((it) => it.kind === "group")
        .map((it) => {
          const g = groups.get(it.id)!
          const src = scene.groupIcons.get(it.id)
          return (
            <g key={it.id} transform={`translate(${it.ax} ${it.ay})`}>
              <GroupShape width={it.width} height={it.height} kind={g.kind} tier={g.tier} scope={g.scope} label={g.label} sublabel={g.sublabel} icon={src && icon(src)} iconWidth={groupIconWidth(g)} />
            </g>
          )
        })}
      {edges.map(({ e, path }) => (
        <EdgeShape key={e.id} path={path} kind={e.kind} />
      ))}
      {diagram.items
        .filter((it) => it.kind !== "group")
        .map((it) => {
          if (it.kind === "card") {
            const c = scene.cards.get(it.id)!
            return (
              <g key={it.id} transform={`translate(${it.ax} ${it.ay})`}>
                <CardShape icon={icon(c.icon)} text={c.text} existing={cards.get(it.id)?.existing} onBorder={cards.get(it.id)?.onBorder} />
              </g>
            )
          }
          const a = actors.get(it.id)!
          return (
            <g key={it.id} transform={`translate(${it.ax} ${it.ay})`}>
              <ActorShape width={it.width} icon={icon(scene.actorIcons.get(it.id)!)} label={a.label} sublabel={a.sublabel} />
            </g>
          )
        })}
      {title && (
        <g transform={`translate(${deco.header.x} ${deco.header.y})`}>
          <HeaderShape title={deco.title} subtitle={deco.subtitle} width={deco.header.width} />
        </g>
      )}
      {legend && (
        <g transform={`translate(${deco.legendRect.x} ${deco.legendRect.y})`}>
          <LegendShape entries={deco.legend} />
        </g>
      )}
    </svg>
  )
}
