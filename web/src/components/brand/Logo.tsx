type Props = {
  className?: string;
  alt?: string;
};

export function Logo({ className = "h-9 w-9", alt = "ViewDock" }: Props) {
  return (
    <img
      src="/viewdock.png"
      alt={alt}
      className={className}
      width={512}
      height={512}
      decoding="async"
    />
  );
}
