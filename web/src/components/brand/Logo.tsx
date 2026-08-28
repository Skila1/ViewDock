export function Logo({ className = "h-8 w-8" }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 256 256" fill="none" aria-hidden>
      <rect width="256" height="256" rx="56" fill="#0c1117" />
      <rect x="10" y="10" width="236" height="236" rx="48" stroke="#1ed760" strokeWidth="6" />
      <path d="M98 72v112l92-56-92-56z" fill="#1ed760" />
    </svg>
  );
}
