'use strict';
let workBindings=[];
async function loadWork() {
  const select=$('work-account');
  const selected=select.value || state.selected;
  select.innerHTML=(state.data?.accounts || []).filter(a=>!a.disabled && a.provider!=='fenjue').map(a=>`<option value="${esc(a.id)}">${esc(label(a))}</option>`).join('');
  select.value=selected; if(!select.value)select.selectedIndex=0;
  workBindings=await api.workStatus(); renderWork();
}
function renderWork() {
  const binding=workBindings.find(b=>b.account_id===$('work-account').value);
  $('work-status').textContent=binding ? `已绑定 · ${binding.endpoint} · 工作区 ${binding.workspace.slice(0,8)}…${binding.busy ? ' · 请求进行中' : ' · 发送时重新核验登录状态'}` : '尚未绑定此账号';
  for(const id of ['work-open','work-bind-owned','work-bind','work-unbind'])$(id).disabled=!$('work-account').value;
}
$('work-setup').onclick=()=>{ switchPage('settings'); void action('work-load',async()=>{await loadWork();$('work-account').value=state.selected;renderWork();$('work-settings').scrollIntoView();}); };
$('work-account').onchange=renderWork;
$('work-open').onclick=()=>action('work-open',async()=>{await api.workOpen($('work-account').value);toast('请登录对应账号，打开“工作”并选择模型与思考档位，再点击“登录完成，绑定”', 'success',12000);});
$('work-bind-owned').onclick=()=>action('work-bind',async()=>{workBindings=await api.workBind({account_id:$('work-account').value,owned:true});renderWork();toast('Work 浏览器已绑定');});
$('work-tabs').onclick=()=>action('work-tabs',async()=>{
 const tabs=await api.workTabs($('work-endpoint').value);
 $('work-target').innerHTML=tabs.map(t=>`<option value="${esc(t.id)}">${esc(t.title)} · ${esc(t.id.slice(0,8))}</option>`).join('');
 if(!tabs.length)toast('该浏览器没有 ChatGPT 标签页','error');
});
$('work-bind').onclick=()=>action('work-bind',async()=>{workBindings=await api.workBind({account_id:$('work-account').value,endpoint:$('work-endpoint').value,target_id:$('work-target').value});renderWork();toast('Work 标签页已绑定');});
$('work-unbind').onclick=()=>action('work-unbind',async()=>{workBindings=await api.workUnbind($('work-account').value);renderWork();});
