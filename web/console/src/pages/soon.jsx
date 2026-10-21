// A section that is planned but not built: the page says which milestone
// brings it rather than 404.
export default function Soon({ title, milestone }) {
  return (
    <div>
      <h1 className="text-xl font-extrabold">{title}</h1>
      <p className="text-ink-2 mt-2 text-sm">Arrives with milestone {milestone}.</p>
    </div>
  )
}
