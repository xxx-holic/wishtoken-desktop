'use strict';
const path=require('node:path'),fs=require('node:fs');
const {spawn}=require('node:child_process');
const readline=require('node:readline');
function localCallback(url,signal) {
  const parsed=new URL(url);
  if(!['http://localhost:1455','http://127.0.0.1:1455'].includes(parsed.origin)||parsed.pathname!=='/auth/callback'||parsed.username||parsed.password)throw new Error('invalid_callback');
  return new Promise((resolve,reject)=>{
    const request=require('node:http').get({hostname:'127.0.0.1',port:1455,path:parsed.pathname+parsed.search,signal,timeout:65000},response=>{
      const chunks=[];let size=0;
      response.on('data',chunk=>{size+=chunk.length;if(size>65536)request.destroy();else chunks.push(chunk);});
      response.on('end',()=>resolve({status:response.statusCode,headers:response.headers,text:Buffer.concat(chunks).toString('utf8')}));
      response.on('error',()=>reject(new Error('callback_error')));
    });
    request.on('timeout',()=>request.destroy());request.on('error',()=>reject(new Error('callback_error')));
  });
}
async function createCurlHTTP({proxy='',signal}) {
  const {app}=require('electron');
  const exe=process.platform==='win32'?'wish-login-http.exe':'wish-login-http';
  const folder=`${{win32:'win',darwin:'mac',linux:'linux'}[process.platform]}-${process.arch}`;
  const binary=app.isPackaged?path.join(process.resourcesPath,'login-helper',exe):path.join(__dirname,'../login-helper',folder,exe);
  const bundled=fs.existsSync(binary);
  if(app.isPackaged&&!bundled){const error=new Error('protocol_helper_missing');error.loginMessage='缺少协议授权组件，请重新解压完整客户端。';throw error;}
  const child=spawn(bundled?binary:(process.env.GPTBRIDGE_PROTOCOL_PYTHON||'python'),bundled?[]:['-u',path.join(__dirname,'../protocol/http_worker.py')],{stdio:['pipe','pipe','ignore'],windowsHide:true,env:{...process.env,PYTHONIOENCODING:'utf-8'}});
  const pending=new Map();let next=0,closed=false;
  function close(){if(closed)return;closed=true;signal.removeEventListener('abort',close);for(const request of pending.values()){clearTimeout(request.timer);request.reject(new Error('protocol_transport_closed'));}pending.clear();child.stdin.destroy();child.kill();}
  child.on('error',close);child.on('exit',close);child.stdin.on('error',close);
  signal.addEventListener('abort',close,{once:true});
  const lines=readline.createInterface({input:child.stdout});
  lines.on('line',line=>{if(line.length>16*1024*1024){close();return;}try{const message=JSON.parse(line),p=pending.get(message.id);if(!p)return;pending.delete(message.id);clearTimeout(p.timer);message.ok?p.resolve(message.data):p.reject(new Error(['network_timeout','proxy_connection_error'].includes(message.error)?message.error:'protocol_transport_error'));}catch{close();}});
  const call=(kind,input={})=>new Promise((resolve,reject)=>{
    if(closed||signal.aborted){reject(new Error('protocol_transport_closed'));return;}
    const id=++next,timer=setTimeout(()=>{pending.delete(id);reject(new Error('protocol_transport_timeout'));close();},40000);
    pending.set(id,{resolve,reject,timer});child.stdin.write(JSON.stringify({id,kind,...input})+'\n');
  });
  try{const init=await call('init',{proxy});return {userAgent:init.userAgent,request:(url,options)=>url.startsWith('http:')?localCallback(url,signal):call('request',{url,options}),cookies:url=>call('cookies',{url}),close:async()=>close()};}
  catch(error){close();throw error;}
}
module.exports={createCurlHTTP,localCallback};
