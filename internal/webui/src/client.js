const protocolVersion=1;
const maxConflictRetries=2;

export function startClient(env){
  const {document,fetch,WebSocket,location,sessionStorage,setTimeout,clearTimeout}=env;
  const byID=id=>document.querySelector(`#${id}`);
  const status=byID("status"),connectionDot=byID("connection-dot"),devicePicker=byID("device-picker"),deviceTrigger=byID("device-trigger"),devices=byID("devices"),roots=byID("roots"),library=byID("library"),queue=byID("queue"),toast=byID("toast"),pendingNode=byID("pending"),breadcrumbs=byID("breadcrumbs");
  const state={revision:0,devices:[],queue:[],policy:{LoopSelected:false,AutoPlayNext:false,AutoPlaySameType:false,GaplessEnabled:false,ImageDurationSeconds:10},selected_device_id:"",selected_media:false,selected_media_name:"",active_media_name:"",selected_subtitle:false,selected_subtitle_name:"",transcode:false,has_session:false,playback_state:"",position:0,duration:0,volume:0,muted:false,media_type:"",artwork_id:""};
  let ws,serial=0,reconnectTimer,shuttingDown=false,connected=false,deviceMenuOpen=false,selectedRoot="",selectedLibraryRow=null,selectedArtworkURL="",parents=[],libraryEntries=[],libraryParent="",libraryCursor="",queueRenderKey="",reloaded=sessionStorage.getItem("go2tv-protocol-reload")==="1";
  const pending=new Map();
  const option=(value,label)=>{const node=document.createElement("option");node.value=value;node.textContent=label;return node};
  const button=(label,action,options={})=>{const node=document.createElement("button");node.type="button";node.textContent=label;node.disabled=!!options.disabled;node.className=options.className||"";node.title=options.title||"";node.ariaLabel=options.ariaLabel||"";node.addEventListener("click",action);return node};
  const actionGroup=(...nodes)=>{const node=document.createElement("div");node.className="row-actions";node.append(...nodes);return node};
  const text=(id,value)=>{byID(id).textContent=value};
  const playbackState=()=>String(state.playback_state||"STOPPED").toUpperCase();
  const hasPending=(type,itemID="")=>[...pending.values()].some(request=>request?.type===type&&(!itemID||request.payload?.item_id===itemID));
  const queueLocked=()=>["LOADING","STOPPING"].includes(playbackState())||[...pending.values()].some(request=>request&&(request.type?.startsWith("queue.")||["player.play","player.pause","player.resume","player.stop"].includes(request.type)));
  const connection=(label,value="")=>{status.textContent=label;connectionDot.dataset.state=value};
  const format=seconds=>{seconds=Math.max(0,Number(seconds)||0);const h=Math.floor(seconds/3600),m=Math.floor(seconds%3600/60),s=Math.floor(seconds%60);return h?`${h}:${String(m).padStart(2,"0")}:${String(s).padStart(2,"0")}`:`${m}:${String(s).padStart(2,"0")}`};
  const kindLabel=kind=>({audio:"Audio",video:"Video",image:"Image"}[kind]||"Media");
  const entryKind=name=>/\.(mp3|flac|wav|m4a|aac|ogg|opus)$/i.test(name)?"Audio":/\.(mp4|mkv|avi|mov|webm|m4v|mpeg|mpg)$/i.test(name)?"Video":/\.(jpe?g|png|gif|webp|bmp)$/i.test(name)?"Image":"Media";
  const mediaGlyph=kind=>({audio:"♪",video:"▶",image:"▧"}[kind]||"•");
  function showToast(message,level="info"){
    const node=document.createElement("p");node.textContent=message||"Request failed";node.dataset.level=level;toast.append(node);
    setTimeout(()=>node.remove(),5000);
  }
  function closeArtwork(){const modal=byID("artwork-modal");byID("artwork-modal-image").removeAttribute("src");if(modal.open)modal.close()}
  function openArtwork(item){const modal=byID("artwork-modal"),image=byID("artwork-modal-image");text("artwork-modal-title",item.name);image.alt=`Artwork for ${item.name}`;image.hidden=false;image.src=item.artwork_url;modal.showModal()}
  function mediaThumbnail(item){
    const thumbnail=document.createElement("button"),image=document.createElement("img"),fallback=document.createElement("span");thumbnail.type="button";thumbnail.className="media-thumbnail";thumbnail.ariaLabel=`View artwork for ${item.name}`;thumbnail.title="View artwork";image.alt="";image.loading="lazy";image.decoding="async";image.src=item.thumbnail_url;fallback.className="thumbnail-fallback";fallback.textContent=mediaGlyph(item.media_kind);fallback.ariaHidden="true";
    image.addEventListener("load",()=>{image.hidden=false;fallback.hidden=true;thumbnail.disabled=false});image.addEventListener("error",()=>{image.hidden=true;fallback.hidden=false;thumbnail.disabled=true});thumbnail.addEventListener("click",()=>openArtwork(item));thumbnail.append(image,fallback);return thumbnail;
  }
  function renderPending(){pendingNode.textContent=pending.size?`${pending.size} working`:"";renderPlayback();renderQueue();renderDevices()}
  function renderDevices(){
    const selected=state.selected_device_id||"",items=state.devices||[],selectedDevice=items.find(device=>device.id===selected),disabled=!connected||shuttingDown||hasPending("devices.select");
    deviceTrigger.replaceChildren();deviceTrigger.dataset.selected=String(!!selectedDevice);deviceTrigger.ariaExpanded=String(deviceMenuOpen);deviceTrigger.disabled=disabled||!items.length;
    if(selectedDevice)appendDeviceContent(deviceTrigger,selectedDevice);else{const label=document.createElement("span");label.className="device-name";label.textContent=items.length?"Select a renderer":"No renderers found";deviceTrigger.append(label)}
    devices.replaceChildren();devices.hidden=!deviceMenuOpen;
    for(const device of items){
      const row=document.createElement("button");row.type="button";row.className="device-option";row.dataset.selected=String(device.id===selected);row.role="option";row.ariaSelected=String(device.id===selected);row.disabled=disabled;row.addEventListener("click",()=>{deviceMenuOpen=false;send("devices.select",{device_id:device.id})});appendDeviceContent(row,device);devices.append(row);
    }
    byID("refresh").disabled=!connected||shuttingDown||hasPending("devices.refresh");
  }
  function appendDeviceContent(node,device){const name=document.createElement("span"),badges=document.createElement("span"),protocol=String(device.protocol||"Renderer");name.className="device-name";name.textContent=device.label;name.title=device.label;badges.className="device-badges";badges.append(deviceBadge(protocol,protocol.toLowerCase()));if((device.capabilities||[]).includes("audio_only"))badges.append(deviceBadge("Audio only","audio-only"));node.append(name,badges)}
  function deviceBadge(label,kind){const badge=document.createElement("span");badge.className="device-badge";badge.dataset.kind=kind;badge.textContent=label;return badge}
  function queueAction(item,locked){
    const current=playbackState(),starting=(item.selected&&current==="LOADING")||hasPending("player.play",item.id);
    if(starting)return {label:"Starting…",disabled:true,run:()=>{}};
    if(item.active&&current==="PLAYING")return {label:"Pause",disabled:locked,run:()=>send("player.pause")};
    if(item.active&&current==="PAUSED")return {label:"Resume",disabled:locked,run:()=>send("player.resume")};
    if(item.active&&current==="STOPPING")return {label:"Stopping…",disabled:true,run:()=>{}};
    return {label:"Play",disabled:!connected||locked,run:()=>send("player.play",{item_id:item.id})};
  }
  function selectQueueItem(item){
    const anotherItemPlaying=playbackState()==="PLAYING"&&(state.queue||[]).some(queued=>queued.active&&queued.id!==item.id);
    send(anotherItemPlaying?"player.play":"queue.select",{item_id:item.id});
  }
  function renderQueue(){
    const items=state.queue||[],locked=queueLocked(),starting=[...pending.values()].filter(request=>request?.type==="player.play").map(request=>request.payload?.item_id??"");
    // Snapshots arrive several times per second during playback; rebuilding identical rows would destroy the button under the cursor and make its hover state flicker.
    const key=JSON.stringify([items,locked,playbackState(),connected,starting]);
    if(key===queueRenderKey)return;
    queueRenderKey=key;
    byID("queue-clear").disabled=!connected||locked||!items.length;
    queue.replaceChildren();text("queue-count",String(items.length));
    if(!items.length){const empty=document.createElement("li");empty.className="empty-state";empty.textContent="Queue is empty — add something from your library.";queue.append(empty);return}
    for(const [index,item] of items.entries()){
      const row=document.createElement("li");row.className="queue-row";if(item.selected)row.dataset.selected="true";if(item.active)row.dataset.active="true";
      const number=document.createElement("span");number.className="queue-index";number.textContent=String(index+1);
      const copy=document.createElement("div");copy.className="entry-copy";const label=document.createElement("strong");label.className="entry-name";label.textContent=item.name||"Untitled media";label.title=label.textContent;copy.append(label);
      const meta=document.createElement("span");meta.className="entry-meta";meta.textContent=item.active?"Now playing":item.selected?"Selected":item.parent||kindLabel(item.kind);copy.append(meta);row.append(number,copy);
      const primary=queueAction(item,locked),removeProtected=item.selected||item.active,removeTitle=item.selected?"Cannot remove selected item":item.active?"Cannot remove active item":"Remove";const actions=actionGroup(button(primary.label,primary.run,{disabled:primary.disabled,className:"queue-primary"}),button("Select",()=>selectQueueItem(item),{disabled:locked||item.selected}),button("↑",()=>send("queue.move",{item_id:item.id,delta:-1}),{disabled:locked||index===0,className:"icon-action",title:"Move up",ariaLabel:`Move ${item.name} up`}),button("↓",()=>send("queue.move",{item_id:item.id,delta:1}),{disabled:locked||index===items.length-1,className:"icon-action",title:"Move down",ariaLabel:`Move ${item.name} down`}),button("×",()=>send("queue.remove",{item_id:item.id}),{disabled:locked||removeProtected,className:"remove-action icon-action",title:removeTitle,ariaLabel:`Remove ${item.name}`}));
      row.append(actions);queue.append(row);
    }
  }
  function renderPlayback(){
    const current=playbackState(),displayState=current.charAt(0)+current.slice(1).toLowerCase();text("playback-state",displayState);text("time",`${format(state.position)} / ${format(state.duration)}`);
    const title=current==="LOADING"?state.selected_media_name:(state.active_media_name||state.selected_media_name);text("now-playing-title",title||"Nothing selected");
    const seek=byID("seek");seek.max=String(Math.max(0,state.duration||0));seek.value=String(Math.min(state.position||0,state.duration||0));seek.disabled=!connected||!state.has_session||!state.duration||current==="LOADING"||current==="STOPPING"||hasPending("player.seek");
    const volume=Math.max(0,Math.min(100,Number(state.volume)||0)),volumePending=hasPending("player.volume"),canControlVolume=connected&&(state.has_session||!!state.selected_device_id),mute=byID("mute");byID("volume-down").disabled=!canControlVolume||volumePending;byID("volume-up").disabled=!canControlVolume||volumePending;mute.textContent=state.muted?"Unmute":"Mute";mute.ariaPressed=String(!!state.muted);mute.disabled=!canControlVolume||hasPending("player.mute");byID("transcode").checked=!!state.transcode;byID("transcode").disabled=!connected||hasPending("player.transcode");
    const mediaLabel=state.selected_media?state.selected_media_name||"Selected media":"No media selected",subtitleLabel=state.selected_subtitle?state.selected_subtitle_name||"Selected":"None",clearSubtitle=byID("subtitle-clear");text("media-selected",mediaLabel);text("subtitle-selected",subtitleLabel);byID("media-selected").title=mediaLabel;byID("subtitle-selected").title=subtitleLabel;clearSubtitle.hidden=!state.selected_subtitle;clearSubtitle.disabled=!connected||hasPending("library.clear_subtitle");
    const play=byID("play-toggle"),stop=byID("stop-button");let command="player.play",label="Play",disabled=!state.selected_media&&!state.queue?.some(item=>item.selected);
    if(current==="PLAYING"){command="player.pause";label="Pause"}else if(current==="PAUSED"){command="player.resume";label="Resume"}else if(current==="LOADING"){label="Starting…";disabled=true}else if(current==="STOPPING"){label="Stopping…";disabled=true}
    play.dataset.command=command;play.textContent=label;play.disabled=!connected||shuttingDown||disabled||hasPending(command);stop.disabled=!connected||shuttingDown||(!state.has_session&&current!=="LOADING")||current==="STOPPING"||hasPending("player.stop");
    const art=byID("artwork"),placeholder=byID("artwork-placeholder"),artworkURL=state.artwork_id?`/api/artwork/${encodeURIComponent(state.artwork_id)}.jpg`:selectedArtworkURL;if(artworkURL){art.src=artworkURL;art.hidden=false;placeholder.hidden=true}else{art.removeAttribute("src");art.hidden=true;placeholder.hidden=false}
  }
  function renderPolicy(){
    const p=state.policy||{};byID("loop").checked=!!p.LoopSelected;byID("autoplay").checked=!!p.AutoPlayNext;byID("same-type").checked=!!p.AutoPlaySameType;byID("gapless").checked=!!p.GaplessEnabled;byID("image-duration").value=String(p.ImageDurationSeconds??10);byID("same-type").disabled=!p.AutoPlayNext;byID("gapless").disabled=!p.AutoPlayNext;
  }
  function selectLibraryMedia(row,item){if(selectedLibraryRow)selectedLibraryRow.dataset.selected="false";selectedLibraryRow=row;row.dataset.selected="true";state.selected_media=true;state.selected_media_name=item.name;state.artwork_id="";selectedArtworkURL=item.artwork_url||"";renderPlayback();send("library.select_media",{root_id:selectedRoot,entry_id:item.id})}
  function renderAll(){renderDevices();renderQueue();renderPlayback();renderPolicy()}
  function mergeSnapshot(payload){Object.assign(state,payload);state.playback_state=payload.playback_state??state.playback_state;state.policy=payload.policy??state.policy;state.revision=payload.revision??state.revision;renderAll()}
  function handle(message){
    if(message.protocol_version!==protocolVersion){if(!reloaded){sessionStorage.setItem("go2tv-protocol-reload","1");location.reload()}else connection("Incompatible server","error");return}
    const p=message.payload||{};
    switch(message.type){
    case "state.snapshot":mergeSnapshot(p);break;
    case "state.devices":state.revision=p.revision??state.revision;state.devices=p.devices||[];renderDevices();break;
    case "state.queue":state.revision=p.revision??state.revision;state.queue=p.queue||[];renderQueue();break;
    case "state.playback":Object.assign(state,{revision:p.revision??state.revision,playback_state:p.state??state.playback_state,position:p.position??state.position,duration:p.duration??state.duration,volume:p.volume??state.volume,muted:p.muted??state.muted,has_session:p.has_session??state.has_session});renderPlayback();renderQueue();break;
    case "state.selection":if(p.artwork_id!==undefined)selectedArtworkURL="";Object.assign(state,{revision:p.revision??state.revision,selected_device_id:p.device_id??state.selected_device_id,selected_media:p.media??state.selected_media,selected_media_name:p.media_name??state.selected_media_name,selected_subtitle:p.subtitle??state.selected_subtitle,selected_subtitle_name:p.subtitle_name??state.selected_subtitle_name,transcode:p.transcode??state.transcode,media_type:p.media_type??state.media_type,artwork_id:p.artwork_id??state.artwork_id});renderDevices();renderPlayback();break;
    case "state.policy":state.revision=p.revision??state.revision;state.policy=p.policy||state.policy;renderPolicy();break;
    case "pending":if(!pending.has(message.id))pending.set(message.id,null);renderPending();break;
    case "ack":pending.delete(message.id);state.revision=p.revision??state.revision;renderPending();break;
    case "error":{const request=pending.get(message.id);pending.delete(message.id);state.revision=p.revision??state.revision;if(p.code==="conflict"&&request&&request.attempt<maxConflictRetries){send(request.type,request.payload,request.attempt+1);break}showToast(p.code==="conflict"?"The app kept changing. Please try that action again.":p.message||p.code||"Request failed","error");renderPending();break}
    case "toast":showToast(p.message,p.level);break;
    case "server.shutdown":shuttingDown=true;connected=false;clearTimeout(reconnectTimer);pending.clear();connection("Server stopped","error");renderPending();break;
    }
  }
  function connect(){clearTimeout(reconnectTimer);pending.clear();connected=false;renderPending();connection("Connecting…");ws=new WebSocket(`${location.protocol==="https:"?"wss":"ws"}://${location.host}/api/ws`);ws.addEventListener("open",()=>{connected=true;connection("Connected","connected");renderPending()});ws.addEventListener("close",()=>{connected=false;pending.clear();renderPending();if(shuttingDown)return;connection("Reconnecting…","error");reconnectTimer=setTimeout(connect,1000)});ws.addEventListener("message",event=>{try{handle(JSON.parse(event.data))}catch{showToast("Invalid server message","error")}})}
  function send(type,payload={},attempt=0){if(ws?.readyState!==WebSocket.OPEN){showToast("Not connected","error");return}const id=String(++serial),clean={...payload};delete clean.expected_revision;pending.set(id,{type,payload:clean,attempt});renderPending();ws.send(JSON.stringify({protocol_version:protocolVersion,type,id,payload:{...clean,expected_revision:state.revision}}));return id}
  function renderBreadcrumbs(){breadcrumbs.replaceChildren();breadcrumbs.append(button("Library",()=>{parents=[];browse()}));for(const [index,parent] of parents.entries())breadcrumbs.append(button(parent.name,()=>{parents=parents.slice(0,index+1);browse(parent.id)}))}
  function renderLibrary(){
    library.replaceChildren();const filter=byID("library-filter").value.trim().toLowerCase(),entries=filter?libraryEntries.filter(item=>item.name.toLowerCase().includes(filter)):libraryEntries;
    if(!entries.length){const empty=document.createElement("li");empty.className="empty-state";empty.textContent=filter?"No matches in this folder.":"This folder is empty.";library.append(empty);renderLoadMore();return}
    for(const item of entries){const row=document.createElement("li"),main=document.createElement("div"),copy=document.createElement("div"),label=document.createElement("strong"),meta=document.createElement("span");row.className="library-row";main.className="entry-main";copy.className="entry-copy";label.className="entry-name";label.textContent=item.name;label.title=item.name;meta.className="entry-meta";meta.textContent=item.kind==="directory"?"Folder":/\.(srt|vtt)$/i.test(item.name)?"Subtitle":kindLabel(item.media_kind)||entryKind(item.name);copy.append(label,meta);if(item.thumbnail_url)main.append(mediaThumbnail(item));else{const icon=document.createElement("span");icon.className="entry-icon";icon.textContent=item.kind==="directory"?"▸":"CC";icon.ariaHidden="true";main.append(icon)}main.append(copy);row.append(main);if(item.kind==="directory")row.append(actionGroup(button("Open",()=>{parents.push({id:item.id,name:item.name});browse(item.id)},{className:"primary-action"})));else if(/\.(srt|vtt)$/i.test(item.name))row.append(actionGroup(button("Use subtitle",()=>send("library.select_subtitle",{root_id:selectedRoot,entry_id:item.id}),{className:"primary-action"})));else row.append(actionGroup(button("Select",()=>selectLibraryMedia(row,item),{className:"primary-action"}),button("Add to queue",()=>send("queue.add",{root_id:selectedRoot,entry_id:item.id}))));library.append(row)}
    renderLoadMore();
  }
  function renderLoadMore(){
    if(!libraryCursor)return;
    const nav=document.createElement("li");nav.className="browser-nav";const more=button("Load more",()=>{more.disabled=true;browse(libraryParent,libraryCursor,true)});nav.append(more);library.append(nav);
  }
  async function browse(parentID="",cursor="",append=false){
    const query=new URLSearchParams({root_id:selectedRoot,limit:"200"});if(parentID)query.set("parent_id",parentID);if(cursor)query.set("cursor",cursor);if(!append){library.replaceChildren();const loading=document.createElement("li");loading.className="empty-state loading-state";loading.textContent="Loading folder…";library.append(loading)}
    try{const response=await fetch(`/api/library?${query}`,{headers:{Accept:"application/json"}}),data=await response.json();if(!response.ok)throw new Error(data.error||"Browse failed");libraryEntries=append?[...libraryEntries,...(data.entries||[])]:data.entries||[];libraryParent=parentID;libraryCursor=data.cursor||"";renderBreadcrumbs();renderLibrary()}catch(error){showToast(error.message,"error");if(append)renderLibrary()}
  }
  function sendPolicy(changed=""){if(changed==="loop"&&byID("loop").checked){byID("autoplay").checked=false;byID("same-type").checked=false;byID("gapless").checked=false}else if(changed==="autoplay"&&byID("autoplay").checked)byID("loop").checked=false;const auto=byID("autoplay").checked;send("playback.policy",{policy:{LoopSelected:byID("loop").checked,AutoPlayNext:auto,AutoPlaySameType:auto&&byID("same-type").checked,GaplessEnabled:auto&&byID("gapless").checked,ImageDurationSeconds:Number(byID("image-duration").value)}})}
  async function boot(){
    const response=await fetch("/api/bootstrap",{headers:{Accept:"application/json"}}),bootstrap=await response.json();if(!response.ok)throw new Error(bootstrap.error||"Bootstrap failed");if(bootstrap.protocol_version!==protocolVersion){if(!reloaded){sessionStorage.setItem("go2tv-protocol-reload","1");location.reload()}else connection("Incompatible server","error");return}
    sessionStorage.removeItem("go2tv-protocol-reload");mergeSnapshot(bootstrap.snapshot);roots.replaceChildren();for(const root of bootstrap.roots||[])roots.append(option(root.id,root.name));selectedRoot=roots.value;await browse();connect();
  }
  roots.addEventListener("change",()=>{selectedRoot=roots.value;parents=[];browse()});byID("refresh").addEventListener("click",()=>send("devices.refresh"));byID("queue-clear").addEventListener("click",()=>send("queue.clear"));
  deviceTrigger.addEventListener("click",()=>{deviceMenuOpen=!deviceMenuOpen;renderDevices()});document.addEventListener("click",event=>{if(deviceMenuOpen&&!event.composedPath().includes(devicePicker)){deviceMenuOpen=false;renderDevices()}});document.addEventListener("keydown",event=>{if(deviceMenuOpen&&event.key==="Escape"){deviceMenuOpen=false;renderDevices();deviceTrigger.focus()}});
  for(const node of document.querySelectorAll("[data-command]"))node.addEventListener("click",()=>send(node.dataset.command));
  byID("seek").addEventListener("change",event=>send("player.seek",{seconds:Number(event.target.value)}));byID("volume-down").addEventListener("click",()=>send("player.volume",{delta:-1}));byID("volume-up").addEventListener("click",()=>send("player.volume",{delta:1}));byID("mute").addEventListener("click",()=>send("player.mute",{muted:!state.muted}));byID("transcode").addEventListener("change",event=>send("player.transcode",{enabled:event.target.checked}));byID("subtitle-clear").addEventListener("click",()=>send("library.clear_subtitle"));byID("library-filter").addEventListener("input",renderLibrary);
  byID("artwork").addEventListener("error",()=>{byID("artwork").hidden=true;byID("artwork-placeholder").hidden=false});byID("artwork-modal-image").addEventListener("error",()=>{showToast("Artwork unavailable","error");closeArtwork()});byID("artwork-modal-close").addEventListener("click",closeArtwork);byID("artwork-modal").addEventListener("click",event=>{if(event.target===byID("artwork-modal"))closeArtwork()});
  for(const id of ["loop","autoplay","same-type","gapless","image-duration"])byID(id).addEventListener("change",()=>sendPolicy(id));
  boot().catch(error=>{connection("Unavailable","error");showToast(error.message,"error")});
  return {state,pending,handle,send,browse};
}
