import type { SVGProps } from "react";

type IconProps = SVGProps<SVGSVGElement> & { size?: number };

function SkipTen({ size = 24, forward, ...rest }: IconProps & { forward: boolean }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.9}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      {...rest}
    >
      {forward ? (
        <>
          <path d="M20.5 12a8.5 8.5 0 1 1-8.5-8.5c2.4 0 4.6 1 6.2 2.6L20.5 8.4" />
          <path d="M20.5 3.6v4.8h-4.8" />
        </>
      ) : (
        <>
          <path d="M3.5 12A8.5 8.5 0 1 0 12 3.5c-2.4 0-4.6 1-6.2 2.6L3.5 8.4" />
          <path d="M3.5 3.6v4.8h4.8" />
        </>
      )}
      <text
        x="12"
        y="15.35"
        textAnchor="middle"
        fontSize="8"
        fontWeight={700}
        fill="currentColor"
        stroke="none"
        style={{ fontFamily: "inherit", letterSpacing: "-0.02em" }}
      >
        10
      </text>
    </svg>
  );
}

export function Replay10(props: IconProps) {
  return <SkipTen forward={false} {...props} />;
}

export function Forward10(props: IconProps) {
  return <SkipTen forward {...props} />;
}
