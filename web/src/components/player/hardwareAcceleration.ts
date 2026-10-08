import { isDiscordActivity } from "@/lib/discordActivity";

/** Shown when the browser refuses a stream it should play, which in practice means hardware acceleration is off. */
export function hardwareAccelerationHelp(discord = isDiscordActivity()): string {
  if (discord) {
    return "This video could not play because hardware acceleration is turned off in Discord. Open Discord Settings, go to System, turn on Enable Hardware Acceleration (the switch goes green), let Discord restart, then open the Activity again.";
  }
  return "This video could not play because hardware acceleration is turned off in your browser. Turn it on in your browser's settings (in Chrome or Edge: Settings, System, Use graphics acceleration when available), restart the browser, then try again.";
}
