import test from "node:test";
import assert from "node:assert/strict";
import {startClient} from "./client.js";

class Node {
  constructor(tag="div"){this.tag=tag;this.children=[];this.listeners={};this.dataset={};this.value="";this.checked=false;this.disabled=false;this.hidden=false;this.textContent="";this.src="";this.open=false}
  append(...nodes){this.children.push(...nodes);if(this.tag==="select"&&!this.value&&nodes[0])this.value=nodes[0].value} replaceChildren(...nodes){this.children=[...nodes];if(this.tag==="select")this.value=nodes[0]?.value||""}
  addEventListener(type,fn){(this.listeners[type]??=[]).push(fn)} emit(type){for(const fn of this.listeners[type]||[])fn({target:this,data:this.data})}
  remove(){this.removed=true} removeAttribute(name){this[name]=""} showModal(){this.open=true} close(){this.open=false} contains(node){return this===node||this.children.some(child=>child.contains?.(node))} focus(){this.focused=true}
}
class FakeSocket extends Node {
  static OPEN=1;static instances=[];
  constructor(url){super("ws");this.url=url;this.readyState=1;this.sent=[];FakeSocket.instances.push(this)}
  send(data){this.sent.push(JSON.parse(data))} message(value){this.data=JSON.stringify(value);this.emit("message")} close(){this.readyState=3;this.emit("close")}
}
function fixture(){
  FakeSocket.instances=[];const ids={},commands=[];
  for(const id of ["status","connection-dot","device-picker","device-trigger","devices","roots","library","queue","toast","pending","playback-state","now-playing-title","time","seek","volume-down","volume-up","mute","transcode","media-selected","subtitle-selected","subtitle-clear","artwork","artwork-placeholder","artwork-modal","artwork-modal-image","artwork-modal-title","artwork-modal-close","loop","autoplay","same-type","gapless","image-duration","refresh","queue-count","queue-clear","breadcrumbs","library-filter","play-toggle","stop-button","theme-toggle"])ids[id]=new Node();
  ids.roots.tag="select";
  ids["play-toggle"].dataset.command="player.play";ids["stop-button"].dataset.command="player.stop";commands.push(ids["play-toggle"],ids["stop-button"]);
  const document={listeners:{},documentElement:new Node("html"),querySelector:selector=>ids[selector.slice(1)],querySelectorAll:selector=>selector==="[data-command]"?commands:[],createElement:tag=>new Node(tag),addEventListener(type,fn){(this.listeners[type]??=[]).push(fn)},emit(type,event){for(const fn of this.listeners[type]||[])fn(event)}};
  const snapshot={revision:3,devices:[{id:"dev-1",label:"<Kitchen>",protocol:"Chromecast",capabilities:["audio_only"]},{id:"dev-2",label:"Living room",protocol:"DLNA"}],selected_device_id:"dev-1",selected_media:false,selected_subtitle:false,queue:[],transcode:false,has_session:false,playback_state:"STOPPED",position:0,duration:0,volume:25,muted:false,policy:{LoopSelected:false,AutoPlayNext:false,AutoPlaySameType:false,GaplessEnabled:false,ImageDurationSeconds:10}};
  const fetch=async url=>({ok:true,json:async()=>url==="/api/bootstrap"?{protocol_version:1,revision:3,snapshot,roots:[{id:"root-1",name:"Media"}]}:{entries:[{id:"media-1",name:"<movie>.mp4",kind:"file",media_kind:"video",thumbnail_url:"/api/thumbnail?entry_id=media-1",artwork_url:"/api/media-artwork?entry_id=media-1"},{id:"sub-1",name:"captions.srt",kind:"file"}]}});
  const store=()=>{const data=new Map();return {getItem:key=>data.get(key)||null,setItem:(key,value)=>data.set(key,value),removeItem:key=>data.delete(key)}};
  const mql={matches:false,listeners:[],addEventListener(type,fn){this.listeners.push(fn)},emit(){for(const fn of this.listeners)fn()}};
  const timers=[];const env={document,fetch,WebSocket:FakeSocket,location:{protocol:"http:",host:"test",reload(){this.reloaded=true}},sessionStorage:store(),localStorage:store(),matchMedia:()=>mql,setTimeout:fn=>(timers.push(fn),timers.length),clearTimeout(){}};
  return {ids,commands,document,env,timers,mql};
}
const settle=()=>new Promise(resolve=>setTimeout(resolve,0));

test("renders safe labels and sends stable queue/media payloads",async()=>{
  const {ids,document,env}=fixture();startClient(env);await settle();const ws=FakeSocket.instances[0];assert.ok(ws);
  assert.equal(ids["device-trigger"].children[0].textContent,"<Kitchen>");assert.equal(ids["device-trigger"].children[1].children[0].textContent,"Chromecast");assert.equal(ids["device-trigger"].children[1].children[1].textContent,"Audio only");assert.equal(ids.devices.hidden,true);ids["device-trigger"].emit("click");document.emit("click",{composedPath:()=>[ids["device-trigger"],ids["device-picker"],document]});assert.equal(ids.devices.hidden,false);assert.equal(ids.devices.children[0].dataset.selected,"true");assert.equal(ids.devices.children[1].children[1].children[0].textContent,"DLNA");assert.equal(ids.library.children[0].children[0].children[1].children[0].textContent,"<movie>.mp4");
  ids.devices.children[1].emit("click");assert.equal(ws.sent[0].type,"devices.select");assert.deepEqual(ws.sent[0].payload,{device_id:"dev-2",expected_revision:3});
  assert.equal(ids.devices.hidden,true);
  ids.library.children[0].children[1].children[1].emit("click");assert.deepEqual(ws.sent[1].payload,{root_id:"root-1",entry_id:"media-1",expected_revision:3});
  ws.message({protocol_version:1,type:"state.queue",payload:{revision:4,queue:[{id:"stable-q",name:"<queued>",kind:"video",selected:true,active:false}]}});
  assert.equal(ids.queue.children[0].children[1].children[0].textContent,"<queued>");ids.queue.children[0].children[2].children[1].emit("click");assert.equal(ws.sent[2].type,"queue.select");assert.equal(ws.sent[2].payload.item_id,"stable-q");
});

test("merges partial playback/policy, clears pending, reports errors and shutdown",async()=>{
  const {ids,env,timers}=fixture();const client=startClient(env);await settle();const ws=FakeSocket.instances[0];ws.emit("open");
  ws.message({protocol_version:1,type:"pending",id:"7",payload:{}});assert.equal(ids.pending.textContent,"1 working");
  ws.message({protocol_version:1,type:"state.playback",payload:{revision:8,state:"PLAYING",position:61,duration:125,volume:44,muted:true,has_session:true}});assert.equal(ids.time.textContent,"1:01 / 2:05");assert.equal(ids.seek.value,"61");assert.equal(ids.mute.textContent,"Unmute");assert.equal(ids["volume-down"].disabled,false);assert.equal(ids["volume-up"].disabled,false);
  ws.message({protocol_version:1,type:"state.playback",payload:{revision:9,volume:100}});assert.equal(ids["volume-down"].disabled,false);assert.equal(ids["volume-up"].disabled,false);
  ws.message({protocol_version:1,type:"state.playback",payload:{revision:10,volume:0}});assert.equal(ids["volume-down"].disabled,false);assert.equal(ids["volume-up"].disabled,false);
  ws.message({protocol_version:1,type:"state.policy",payload:{revision:11,policy:{LoopSelected:false,AutoPlayNext:true,AutoPlaySameType:true,GaplessEnabled:false,ImageDurationSeconds:7}}});assert.equal(ids.autoplay.checked,true);assert.equal(ids["same-type"].disabled,false);
  ws.message({protocol_version:1,type:"error",id:"7",payload:{code:"invalid",message:"Try again"}});assert.equal(client.pending.size,0);assert.equal(ids.toast.children[0].textContent,"Try again");
  client.send("player.stop");assert.equal(client.pending.size,1);ws.close();assert.equal(client.pending.size,0);timers.at(-1)();
  const ws2=FakeSocket.instances[1];assert.ok(ws2);ws2.message({protocol_version:1,type:"server.shutdown",payload:{}});assert.equal(ids.status.textContent,"Server stopped");
});

test("control interactions emit typed payloads and subtitle selection",async()=>{
  const {ids,env}=fixture();startClient(env);await settle();const ws=FakeSocket.instances[0];
  ids.library.children[1].children[1].children[0].emit("click");
  ids.seek.value="42";ids.seek.emit("change");ids["volume-up"].emit("click");ids["volume-down"].emit("click");ids.mute.emit("click");ids.transcode.checked=true;ids.transcode.emit("change");
  ids.autoplay.checked=true;ids["same-type"].checked=true;ids["image-duration"].value="12";ids["same-type"].emit("change");
  assert.deepEqual(ws.sent.map(message=>[message.type,message.payload]),[
    ["library.select_subtitle",{root_id:"root-1",entry_id:"sub-1",expected_revision:3}],
    ["player.seek",{seconds:42,expected_revision:3}],
    ["player.volume",{delta:1,expected_revision:3}],
    ["player.volume",{delta:-1,expected_revision:3}],
    ["player.mute",{muted:true,expected_revision:3}],
    ["player.transcode",{enabled:true,expected_revision:3}],
    ["playback.policy",{policy:{LoopSelected:false,AutoPlayNext:true,AutoPlaySameType:true,GaplessEnabled:false,ImageDurationSeconds:12},expected_revision:3}],
  ]);
});

test("protocol mismatch reloads at most once",async()=>{
  const first=fixture();startClient(first.env);await settle();FakeSocket.instances[0].message({protocol_version:2,type:"state.snapshot",payload:{}});assert.equal(first.env.location.reloaded,true);
  const second=fixture();second.env.sessionStorage.setItem("go2tv-protocol-reload","1");startClient(second.env);await settle();FakeSocket.instances[0].message({protocol_version:2,type:"state.snapshot",payload:{}});assert.equal(second.env.location.reloaded,undefined);assert.equal(second.ids.status.textContent,"Incompatible server");
});

test("shows selected names and silently retries revision conflicts",async()=>{
  const {ids,env}=fixture();const client=startClient(env);await settle();const ws=FakeSocket.instances[0];
  ids.library.children[0].children[1].children[0].emit("click");assert.equal(ids.library.children[0].dataset.selected,"true");assert.equal(ids["media-selected"].textContent,"<movie>.mp4");
  ws.message({protocol_version:1,type:"state.selection",payload:{revision:4,media:true,media_name:"long song.flac",subtitle:true,subtitle_name:"captions.srt"}});
  assert.equal(ids["media-selected"].textContent,"long song.flac");assert.equal(ids["subtitle-selected"].textContent,"captions.srt");assert.equal(ids["subtitle-clear"].hidden,false);ids["subtitle-clear"].emit("click");assert.equal(ws.sent.at(-1).type,"library.clear_subtitle");assert.deepEqual(ws.sent.at(-1).payload,{expected_revision:4});
  ws.message({protocol_version:1,type:"state.selection",payload:{revision:5,subtitle:false,subtitle_name:""}});assert.equal(ids["subtitle-selected"].textContent,"None");assert.equal(ids["subtitle-clear"].hidden,true);
  const first=client.send("player.seek",{seconds:12});
  ws.message({protocol_version:1,type:"error",id:first,payload:{code:"conflict",message:"state changed",revision:5}});
  assert.equal(ws.sent.at(-1).type,"player.seek");assert.deepEqual(ws.sent.at(-1).payload,{seconds:12,expected_revision:5});assert.equal(ids.toast.children.length,0);
  const retry=ws.sent.at(-1).id;ws.message({protocol_version:1,type:"error",id:retry,payload:{code:"conflict",message:"state changed",revision:6}});
  assert.deepEqual(ws.sent.at(-1).payload,{seconds:12,expected_revision:6});assert.equal(ids.toast.children.length,0);
  const finalRetry=ws.sent.at(-1).id;ws.message({protocol_version:1,type:"error",id:finalRetry,payload:{code:"conflict",message:"state changed",revision:7}});
  assert.equal(ids.toast.children[0].textContent,"The app kept changing. Please try that action again.");
});

test("loads browser thumbnails, opens artwork modal and updates player artwork",async()=>{
  const {ids,env}=fixture();startClient(env);await settle();const row=ids.library.children[0],thumbnail=row.children[0].children[0];
  assert.equal(thumbnail.children[0].src,"/api/thumbnail?entry_id=media-1");thumbnail.children[0].emit("load");assert.equal(thumbnail.children[0].hidden,false);thumbnail.emit("click");
  assert.equal(ids["artwork-modal"].open,true);assert.equal(ids["artwork-modal-image"].src,"/api/media-artwork?entry_id=media-1");assert.equal(ids["artwork-modal-title"].textContent,"<movie>.mp4");
  ids["artwork-modal-close"].emit("click");assert.equal(ids["artwork-modal"].open,false);row.children[1].children[0].emit("click");assert.equal(ids.artwork.src,"/api/media-artwork?entry_id=media-1");
  FakeSocket.instances[0].message({protocol_version:1,type:"state.selection",payload:{revision:4,artwork_id:"cover-id",media_type:"video"}});assert.equal(ids.artwork.src,"/api/artwork/cover-id.jpg");assert.equal(ids["artwork-placeholder"].hidden,true);
});

test("queue and primary transport follow loading and active state",async()=>{
  const {ids,env}=fixture();startClient(env);await settle();const ws=FakeSocket.instances[0];
  ws.message({protocol_version:1,type:"state.snapshot",payload:{revision:4,selected_media:true,selected_media_name:"next.flac",active_media_name:"current.flac",has_session:true,playback_state:"PLAYING",queue:[{id:"q1",name:"current.flac",kind:"audio",selected:true,active:true}]}});
  assert.equal(ids["now-playing-title"].textContent,"current.flac");assert.equal(ids["play-toggle"].textContent,"Pause");assert.equal(ids.queue.children[0].children[2].children[0].textContent,"Pause");
  assert.equal(ids.queue.children[0].children[2].children[4].disabled,true);assert.equal(ids.queue.children[0].children[2].children[4].title,"Cannot remove selected item");
  ids.queue.children[0].children[2].children[0].emit("click");assert.equal(ws.sent.at(-1).type,"player.pause");
  ws.message({protocol_version:1,type:"state.snapshot",payload:{revision:5,selected_media:true,selected_media_name:"next.flac",active_media_name:"current.flac",has_session:true,playback_state:"LOADING",queue:[{id:"q1",name:"current.flac",kind:"audio",selected:false,active:true},{id:"q2",name:"next.flac",kind:"audio",selected:true,active:false}]}});
  assert.equal(ids["now-playing-title"].textContent,"next.flac");assert.equal(ids.queue.children[1].children[2].children[0].textContent,"Starting…");assert.equal(ids.queue.children[1].children[1].children[0].textContent,"next.flac");
});

test("selecting another queue item replaces active playback",async()=>{
  const {ids,env}=fixture();startClient(env);await settle();const ws=FakeSocket.instances[0];ws.emit("open");
  ws.message({protocol_version:1,type:"state.snapshot",payload:{revision:4,has_session:true,playback_state:"PLAYING",queue:[{id:"q1",name:"one.mp3",kind:"audio",selected:true,active:true},{id:"q2",name:"two.mp3",kind:"audio",selected:false,active:false}]}});
  ids.queue.children[1].children[2].children[1].emit("click");
  assert.equal(ws.sent.at(-1).type,"player.play");assert.deepEqual(ws.sent.at(-1).payload,{item_id:"q2",expected_revision:4});
});

test("queue locks every row during mutations and transitions",async()=>{
  const {ids,env}=fixture();startClient(env);await settle();const ws=FakeSocket.instances[0];ws.emit("open");
  ws.message({protocol_version:1,type:"state.queue",payload:{revision:4,queue:[{id:"q1",name:"one.mp3",kind:"audio",selected:true,active:false},{id:"q2",name:"two.mp3",kind:"audio",selected:false,active:false}]}});
  assert.equal(ids.queue.children[0].children[2].children[4].disabled,true);assert.equal(ids.queue.children[0].children[2].children[4].title,"Cannot remove selected item");assert.equal(ids.queue.children[1].children[2].children[4].disabled,false);
  assert.equal(ids.queue.children[1].children[2].children[0].disabled,false);ids.queue.children[0].children[2].children[0].emit("click");
  for(const row of ids.queue.children)for(const control of row.children[2].children)assert.equal(control.disabled,true);
  const request=ws.sent.at(-1).id;ws.message({protocol_version:1,type:"ack",id:request,payload:{revision:5}});assert.equal(ids.queue.children[1].children[2].children[0].disabled,false);
  ws.message({protocol_version:1,type:"state.playback",payload:{revision:6,state:"LOADING",has_session:true}});for(const row of ids.queue.children)for(const control of row.children[2].children)assert.equal(control.disabled,true);
});

test("load more survives filtering and appends the next page",async()=>{
  const {ids,env}=fixture();const bootstrapFetch=env.fetch;
  env.fetch=async url=>{
    if(!url.startsWith("/api/library"))return bootstrapFetch(url);
    const cursor=new URL(`http://host${url}`).searchParams.get("cursor")||"";
    return {ok:true,json:async()=>cursor?{entries:[{id:"media-2",name:"second.mp4",kind:"file",media_kind:"video"}]}:{entries:[{id:"media-1",name:"first.mp4",kind:"file",media_kind:"video"}],cursor:"next"}};
  };
  startClient(env);await settle();
  const loadMore=()=>ids.library.children.find(node=>node.className==="browser-nav");
  assert.ok(loadMore(),"load more visible after first page");
  ids["library-filter"].value="zzz";ids["library-filter"].emit("input");
  assert.equal(ids.library.children[0].textContent,"No matches in this folder.");assert.ok(loadMore(),"load more visible while filter hides everything");
  ids["library-filter"].value="";ids["library-filter"].emit("input");
  loadMore().children[0].emit("click");await settle();
  assert.equal(loadMore(),undefined);assert.deepEqual(ids.library.children.map(row=>row.children[0]?.children[1]?.children[0]?.textContent),["first.mp4","second.mp4"]);
});

test("orders library entries by name and marks folders with an icon",async()=>{
  const {ids,env}=fixture();const bootstrapFetch=env.fetch;
  env.fetch=async url=>{
    if(!url.startsWith("/api/library"))return bootstrapFetch(url);
    const cursor=new URL(`http://host${url}`).searchParams.get("cursor")||"";
    return {ok:true,json:async()=>cursor?{entries:[{id:"m-4",name:"Episode 2.mkv",kind:"file",media_kind:"video"}]}:{entries:[{id:"m-2",name:"zebra.mp4",kind:"file",media_kind:"video"},{id:"dir-1",name:"Concerts",kind:"directory"},{id:"m-3",name:"Episode 10.mkv",kind:"file",media_kind:"video"},{id:"m-1",name:"alpha.mp3",kind:"file",media_kind:"audio"}],cursor:"next"}};
  };
  startClient(env);await settle();
  const names=()=>ids.library.children.filter(row=>row.className==="library-row").map(row=>row.children[0].children[1].children[0].textContent);
  assert.deepEqual(names(),["alpha.mp3","Concerts","Episode 10.mkv","zebra.mp4"]);
  const folderIcon=ids.library.children[1].children[0].children[0];
  assert.equal(folderIcon.className,"entry-icon folder-icon");assert.equal(folderIcon.textContent,"");assert.equal(ids.library.children[1].children[0].children[1].children[1].textContent,"Folder");
  assert.equal(ids.library.children[0].children[0].children[0].className,"entry-icon");
  ids.library.children.find(node=>node.className==="browser-nav").children[0].emit("click");await settle();
  assert.deepEqual(names(),["alpha.mp3","Concerts","Episode 2.mkv","Episode 10.mkv","zebra.mp4"]);
});

test("clear queue button sends queue.clear and tracks queue state",async()=>{
  const {ids,env}=fixture();startClient(env);await settle();const ws=FakeSocket.instances[0];ws.emit("open");
  assert.equal(ids["queue-clear"].disabled,true);
  ws.message({protocol_version:1,type:"state.queue",payload:{revision:4,queue:[{id:"q1",name:"one.mp3",kind:"audio",selected:false,active:false}]}});
  assert.equal(ids["queue-clear"].disabled,false);
  ids["queue-clear"].emit("click");
  assert.equal(ws.sent.at(-1).type,"queue.clear");assert.deepEqual(ws.sent.at(-1).payload,{expected_revision:4});
  assert.equal(ids["queue-clear"].disabled,true);
  ws.message({protocol_version:1,type:"ack",id:ws.sent.at(-1).id,payload:{revision:5}});
  ws.message({protocol_version:1,type:"state.queue",payload:{revision:5,queue:[]}});
  assert.equal(ids["queue-clear"].disabled,true);assert.equal(ids["queue-count"].textContent,"0");assert.equal(ids.queue.children[0].textContent,"Queue is empty — add something from your library.");
});

test("clear queue retains active item controls and artwork",async()=>{
  const {ids,env}=fixture();startClient(env);await settle();const ws=FakeSocket.instances[0];ws.emit("open");
  const active={id:"q1",name:"one.mp3",kind:"audio",selected:true,active:true};
  ws.message({protocol_version:1,type:"state.snapshot",payload:{revision:4,selected_media:true,selected_media_name:"one.mp3",active_media_name:"one.mp3",artwork_id:"cover",has_session:true,playback_state:"PLAYING",queue:[active,{id:"q2",name:"two.mp3",kind:"audio",selected:false,active:false}]}});
  ids["queue-clear"].emit("click");const request=ws.sent.at(-1);assert.equal(request.type,"queue.clear");
  ws.message({protocol_version:1,type:"ack",id:request.id,payload:{revision:5}});
  ws.message({protocol_version:1,type:"state.snapshot",payload:{revision:5,selected_media:true,selected_media_name:"one.mp3",active_media_name:"one.mp3",artwork_id:"cover",has_session:true,playback_state:"PLAYING",queue:[active]}});
  assert.equal(ids.queue.children.length,1);assert.equal(ids.queue.children[0].children[1].children[0].textContent,"one.mp3");
  assert.equal(ids.artwork.src,"/api/artwork/cover.jpg");assert.equal(ids["artwork-placeholder"].hidden,true);
  assert.equal(ids["play-toggle"].textContent,"Pause");assert.equal(ids["play-toggle"].disabled,false);assert.equal(ids.queue.children[0].children[2].children[0].disabled,false);
});

test("theme toggle cycles auto, light, dark and follows the system only in auto",async()=>{
  const {ids,document,env,mql}=fixture();mql.matches=true;startClient(env);await settle();
  const toggle=ids["theme-toggle"],theme=()=>document.documentElement.dataset.theme;
  assert.equal(theme(),"dark");assert.equal(toggle.dataset.mode,"auto");assert.equal(toggle.title,"Theme: Auto");
  toggle.emit("click");assert.equal(theme(),"light");assert.equal(env.localStorage.getItem("go2tv-theme"),"light");assert.equal(toggle.title,"Theme: Light");
  mql.emit();assert.equal(theme(),"light");
  toggle.emit("click");assert.equal(theme(),"dark");assert.equal(env.localStorage.getItem("go2tv-theme"),"dark");assert.equal(toggle.dataset.mode,"dark");
  toggle.emit("click");assert.equal(env.localStorage.getItem("go2tv-theme"),"auto");assert.equal(theme(),"dark");
  mql.matches=false;mql.emit();assert.equal(theme(),"light");
  const second=fixture();second.env.localStorage.setItem("go2tv-theme","dark");startClient(second.env);await settle();
  assert.equal(second.document.documentElement.dataset.theme,"dark");assert.equal(second.ids["theme-toggle"].title,"Theme: Dark");
});

test("loop and autoplay remain mutually exclusive",async()=>{
  const {ids,env}=fixture();startClient(env);await settle();const ws=FakeSocket.instances[0];
  ids.autoplay.checked=true;ids["same-type"].checked=true;ids.loop.checked=true;ids.loop.emit("change");
  assert.equal(ids.autoplay.checked,false);assert.equal(ids["same-type"].checked,false);assert.deepEqual(ws.sent.at(-1).payload.policy,{LoopSelected:true,AutoPlayNext:false,AutoPlaySameType:false,GaplessEnabled:false,ImageDurationSeconds:10});
  ids.autoplay.checked=true;ids.autoplay.emit("change");assert.equal(ids.loop.checked,false);assert.equal(ws.sent.at(-1).payload.policy.AutoPlayNext,true);
});
