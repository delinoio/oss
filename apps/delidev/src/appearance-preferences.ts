// SPDX-License-Identifier: Apache-2.0
import palettes from "./appearance-palettes.json";
export enum Palette { Default="default", Titanium="titanium", Nord="nord", Dracula="dracula", Solarized="solarized" }
export enum Layout { Regular="regular", Compact="compact" }
export enum StatusPresentation { Default="default", Minimal="minimal" }
export enum ImageSize { Fit="fit", Original="original" }
export enum DisclosureDefault { Original="original", Collapsed="collapsed", Expanded="expanded" }
export type TextSize = 0 | 12 | 14 | 16 | 18;
export const colorTokens = Object.keys(palettes.default.light) as (keyof typeof palettes.default.light)[];
export type ColorMap = Record<(typeof colorTokens)[number], string>;
export interface ThemeFile { version: 1; name: string; light: ColorMap; dark: ColorMap }
export interface CustomTheme extends ThemeFile { id: string }
export interface AppearancePreferences {
 light_palette:string; dark_palette:string; color_assistance:boolean; custom_themes:CustomTheme[];
 composer_layout:Layout; composer_size:TextSize; status:StatusPresentation; session_accent:boolean; show_tokens:boolean; show_time:boolean;
 density:Layout; conversation_size:TextSize; animation:boolean; tool_disclosure:DisclosureDefault; reasoning_disclosure:DisclosureDefault; compaction_disclosure:DisclosureDefault;
 markdown:boolean; mermaid:boolean; svg:boolean; table_charts:boolean; inline_images:boolean; image_size:ImageSize;
}
export function defaultPreferences():AppearancePreferences { return {light_palette:Palette.Default,dark_palette:Palette.Default,color_assistance:false,custom_themes:[],composer_layout:Layout.Regular,composer_size:0,status:StatusPresentation.Default,session_accent:true,show_tokens:true,show_time:true,density:Layout.Regular,conversation_size:0,animation:true,tool_disclosure:DisclosureDefault.Original,reasoning_disclosure:DisclosureDefault.Original,compaction_disclosure:DisclosureDefault.Original,markdown:true,mermaid:true,svg:true,table_charts:true,inline_images:true,image_size:ImageSize.Fit}; }
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==="object"&&!Array.isArray(v);
const exact=(v:Record<string,unknown>,keys:string[])=>Object.keys(v).sort().join(",")===keys.sort().join(",");
const luminance=(hex:string)=>{const values=[1,3,5].map(i=>parseInt(hex.slice(i,i+2),16)/255).map(x=>x<=.04045?x/12.92:((x+.055)/1.055)**2.4);return .2126*values[0]+.7152*values[1]+.0722*values[2];};
export function contrast(a:string,b:string) { const x=luminance(a),y=luminance(b); return (Math.max(x,y)+.05)/(Math.min(x,y)+.05); }
export function validColors(value:unknown):value is ColorMap {
 if(!object(value)||!exact(value,[...colorTokens])||!Object.values(value).every(v=>typeof v==="string"&&/^#[0-9A-Fa-f]{6}$/.test(v)))return false;
 const v=value as ColorMap;
 return ([["text","surface"],["text-secondary","surface"],["muted","surface"],["text-subtle","surface"],["on-accent","accent"],["on-accent","accent-hover"],["selected-text","selected-background"],["warning-text","warning-background"],["danger-text","danger-background"],["on-inverse","inverse-surface"]] as [keyof ColorMap,keyof ColorMap][]).every(([a,b])=>contrast(v[a],v[b])>=4.5)&&contrast(v["control-border"],v.surface)>=3;
}
export function validTheme(value:unknown,identity=false):value is ThemeFile|CustomTheme {
 return object(value)&&exact(value,identity?["version","name","light","dark","id"]:["version","name","light","dark"])&&value.version===1&&typeof value.name==="string"&&value.name.trim().length>0&&[...value.name].length<=80&&!/[\u0000-\u001f\u007f-\u009f\uD800-\uDFFF]/u.test(value.name)&&(!identity||typeof value.id==="string"&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value.id))&&validColors(value.light)&&validColors(value.dark);
}
export function parseThemeFile(bytes:string):ThemeFile { if(new TextEncoder().encode(bytes).length>32768)throw new Error("Invalid theme");const value:unknown=JSON.parse(bytes);if(!validTheme(value))throw new Error("Invalid theme");return value; }
export function parsePreferences(value:unknown):AppearancePreferences {
 const defaults=defaultPreferences();if(!object(value)||!exact(value,Object.keys(defaults)))throw new Error("Invalid preferences");
 for(const key of Object.keys(defaults) as (keyof AppearancePreferences)[]){const v=value[key];if(typeof defaults[key]==="boolean"&&typeof v!=="boolean")throw new Error("Invalid preferences");}
 if(![0,12,14,16,18].includes(value.composer_size as number)||![0,12,14,16,18].includes(value.conversation_size as number)||![value.density,value.composer_layout].every(v=>Object.values(Layout).includes(v as Layout))||!Object.values(StatusPresentation).includes(value.status as StatusPresentation)||!Object.values(ImageSize).includes(value.image_size as ImageSize)||![value.tool_disclosure,value.reasoning_disclosure,value.compaction_disclosure].every(v=>Object.values(DisclosureDefault).includes(v as DisclosureDefault))||!Array.isArray(value.custom_themes)||value.custom_themes.length>32||!value.custom_themes.every(v=>validTheme(v,true)))throw new Error("Invalid preferences");
 const ids=value.custom_themes.map(v=>(v as CustomTheme).id);if(new Set(ids).size!==ids.length||![value.light_palette,value.dark_palette].every(v=>typeof v==="string"&&(Object.values(Palette).includes(v as Palette)||ids.includes(v))))throw new Error("Invalid preferences");return value as unknown as AppearancePreferences;
}
export function selectedColors(preferences:AppearancePreferences,dark:boolean):ColorMap { const id=dark?preferences.dark_palette:preferences.light_palette;const theme=preferences.custom_themes.find(t=>t.id===id)??palettes[id as Palette];return theme[dark?"dark":"light"]; }
export { palettes };

// Native maps serialize in key order. Object property order grants no revision authority.
export function appearanceIdentity(value: unknown): string {
 const canonical=(v:unknown):unknown=>Array.isArray(v)?v.map(canonical):v && typeof v==="object"?Object.fromEntries(Object.entries(v).sort(([a],[b])=>a.localeCompare(b)).map(([k,x])=>[k,canonical(x)])):v;
 return JSON.stringify(canonical(value));
}

/** Only application-owned selectors and previously validated colors reach CSSOM. */
export function applyAppearanceColors(colors:ColorMap,selector=":root"):()=>void {
 if(typeof CSSStyleSheet.prototype.replaceSync!=="function")return ()=>{};
 const sheet=new CSSStyleSheet();sheet.replaceSync(`${selector} {}`);const rule=sheet.cssRules[0] as CSSStyleRule;
 for(const [token,value] of Object.entries(colors))rule.style.setProperty(`--${token}`,value);
 document.adoptedStyleSheets=[...document.adoptedStyleSheets,sheet];
 return ()=>{document.adoptedStyleSheets=document.adoptedStyleSheets.filter(value=>value!==sheet);};
}
