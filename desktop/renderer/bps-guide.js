'use strict';
async function prepareBPSCandy(id){
  await loadPelican();
  if(!pelicanState.prompts.candy)throw new Error('测试题未加载，请重试');
  $('pelican-channel').value='bps';channelModels('bps',$('pelican-model'),'gpt-6-astra');
  $('pelican-benchmark').value='candy';$('pelican-prompt').value=pelicanState.prompts.candy;$('pelican-prompt').rows=8;
  $('pelican-effort').value='medium';$('pelican-concurrency').value='1';pelicanState.selected=new Set(id?[id]:[]);
  pelicanChannelOptions();pelicanAccounts();renderPelican();switchPage('pelican');
  toast('已准备原题与 BPS 通道，请核对账号后点击开始测智');
}
function showSavedBPS(id){
  const account=accountByID(id);if(!account)return;
  modal(`<h2>BPS 授权已保存</h2><p>${esc(label(account))}</p><p>${account.disabled?'此记录仍处于停用状态。重新授权保留原有暂停设置；继续测试会先启用此账号。':'现在可以直接通过 BPS 测试，不需要另拿原生授权。'}</p><p class="small">授权成功只表示凭据已保存；是否可请求、题目是否答对，需要分别验证。</p><div class="modal-actions"><button class="secondary" id="bps-saved-close">稍后测试</button><button class="primary" id="bps-saved-test">${account.disabled?'启用并准备糖果题':'准备糖果题'}</button></div>`);
  $('bps-saved-close').onclick=closeModal;
  $('bps-saved-test').onclick=()=>action('bps-prepare',async()=>{if(account.disabled)await api.account({id,action:'update',disabled:false});closeModal();await refresh(true);await prepareBPSCandy(id);});
}
$('bps-guide-login').onclick=()=>manualLoginDialog();
$('bps-guide-test').onclick=()=>action('bps-guide-test',()=>prepareBPSCandy(grantType(accountByID(state.selected))==='bps'?state.selected:''));
