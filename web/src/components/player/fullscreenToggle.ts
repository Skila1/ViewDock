/** Chrome is fullscreen if any of these are on — including local `fs` after a tap. */
export function shouldExitFullscreen(opts: {
  documentFs: boolean;
  nativeFs: boolean;
  pageFs: boolean;
  chromeFs: boolean;
}): boolean {
  return opts.documentFs || opts.nativeFs || opts.pageFs || opts.chromeFs;
}
