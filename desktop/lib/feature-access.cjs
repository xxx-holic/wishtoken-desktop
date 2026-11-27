'use strict';
async function requireFenjue(service) {
  const status=await service.request('/api/features/fenjue/license');
  if(status.unlocked!==true)throw new Error('焚诀通道已锁定，请先在设置中输入有效功能授权码。');
  return status;
}
function licenseCode(value){
  if(typeof value!=='string'||value.trim().length>8192||!/^WTK1\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(value.trim()))throw new Error('授权码格式不正确，请完整粘贴签发方提供的授权码。');
  return value.trim();
}
module.exports={requireFenjue,licenseCode};
