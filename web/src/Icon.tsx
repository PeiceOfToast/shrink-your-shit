// Icons come from design's sprite (icons.svg, put in the page once by App).
export function Icon({ id }: { id: string }) {
  return (
    <svg className="ic" aria-hidden="true">
      <use href={"#" + id} />
    </svg>
  );
}
