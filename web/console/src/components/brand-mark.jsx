// The brand's mark (../gathering_brand/mark): two heads over two bodies that
// lean in towards each other, drawn from the brand's own geometry
// (gathering_brand/source/build.py, `mark_parts`) in the text's colour.
export default function BrandMark({ className }) {
  return (
    <svg viewBox="-46.04 -0.11 92.08 71.79" fill="currentColor" aria-hidden="true" className={className}>
      <rect x="-10.3" y="-10.3" width="20.6" height="20.6" rx="4" transform="translate(-18.5 12.8) rotate(45)" />
      <rect x="-10.3" y="-10.3" width="20.6" height="20.6" rx="4" transform="translate(18.5 12.8) rotate(45)" />
      <rect x="-13" y="-22.3" width="26" height="44.6" rx="9.3" transform="translate(-25 50.5) rotate(44.4)" />
      <rect x="-13" y="-22.3" width="26" height="44.6" rx="9.3" transform="translate(25 50.5) rotate(-44.4)" />
    </svg>
  )
}
