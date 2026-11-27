'use strict';
function renderFeatureLicense(){
  const s=state.data?.features?.fenjue||{unlocked:false,reason:'not_activated'};
  const reasons={not_activated:'尚未解锁',expired:'授权已到期',wrong_installation:'授权码与此安装不匹配',invalid_signature:'授权码签名无效',invalid_code:'授权码格式无效',invalid_claims:'授权码内容无效',storage_error:'本地授权存储不可用',issuer_unavailable:'验签配置不可用',not_yet_valid:'请检查系统时间'};
  $('feature-license-status').textContent=s.unlocked?`已解锁 · 有效期至 ${date(new Date(s.expires_at*1000).toISOString())}`:`${reasons[s.reason]||'未解锁'} · BPS 和原生通道不受影响`;
  $('feature-installation').value=s.installation||'';
  $('feature-copy').disabled=!s.installation;
  $('feature-deactivate').hidden=!s.unlocked;
  for(const id of ['fenjue-login','fenjue-byok','fenjue-bind'])$(id).disabled=!s.unlocked;
  $('fenjue-check').disabled=!s.unlocked||!$('fenjue-account').value;
  for(const id of ['channel','pelican-channel']){const o=$(id).querySelector('option[value="fenjue"]');if(o)o.textContent=s.unlocked?'焚诀订阅通道':'焚诀订阅通道 · 需授权码';}
}
$('feature-copy').onclick=()=>action('feature-copy',async()=>{await api.featureCopyInstallation();toast('安装标识已复制，请提供给授权码签发方');});
$('feature-activate').onclick=()=>action('feature-activate',async()=>{
  const button=$('feature-activate');button.disabled=true;$('feature-error').hidden=true;
  try{await api.featureActivate($('feature-code').value);$('feature-code').value='';await refresh(true);renderFeatureLicense();toast('焚诀功能已解锁');}
  catch(e){$('feature-error').textContent=e.message;$('feature-error').hidden=false;$('feature-code').setAttribute('aria-invalid','true');$('feature-code').focus();}
  finally{button.disabled=false;}
});
$('feature-code').oninput=()=>{$('feature-code').removeAttribute('aria-invalid');};
$('feature-deactivate').onclick=()=>action('feature-deactivate',async()=>{await api.featureDeactivate();await refresh(true);renderFeatureLicense();toast('本地功能授权已移除，账号凭据保留');});
function openFeatureLicense(){switchPage('settings');renderFeatureLicense();$('fenjue-license-settings').scrollIntoView({block:'start'});$('feature-code').focus();}
renderFeatureLicense();
