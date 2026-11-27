'use strict';
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { createHash, createHmac, randomUUID } = require('node:crypto');

const FILES = ['config.toml', 'auth.json', 'wishtoken-models.json', 'bridge-identity.json'];
const MANAGED = ['model_provider', 'model', 'model_reasoning_effort', 'service_tier', 'model_catalog_json', 'model_context_window', 'model_auto_compact_token_limit', 'cli_auth_credentials_store', 'openai_base_url'];
// Codex compresses ChatGPT-auth request bodies with zstd by default. The local
// bridge accepts identity and gzip only, so the managed config disables it and
// restore puts the user's original feature value back.
const FEATURE_FLAG = 'enable_request_compression';
function mainHome(env = process.env, userHome = os.homedir()) {
  const value = env.GPTBRIDGE_MAIN_CODEX_HOME || env.CODEX_HOME || path.join(userHome, '.codex');
  if (!path.isAbsolute(value)) throw new Error('主 Codex 数据目录必须是绝对路径');
  return path.resolve(value);
}
function scalar(value) {
  const text = value.trim().replace(/\s+#.*$/, '').trim();
  if (text.startsWith('"')) { try { return JSON.parse(text); } catch { return undefined; } }
  if (/^'[^']*'$/.test(text)) return text.slice(1, -1);
  return undefined;
}
function tablePath(text) {
  const items = text.trim().match(/(?:"(?:\\.|[^"\\])*"|'[^']*'|[A-Za-z0-9_-]+)(?:\s*\.|\s*$)/g);
  if (!items) throw new Error('配置中存在暂不支持的表名，请先整理 config.toml');
  const value = items.map(s => s.trim().replace(/\.$/, '').trim());
  if (value.join('.') !== text.replace(/\s*\.\s*/g, '.').trim()) throw new Error('无法安全解析配置表名');
  return value.map(s => s.startsWith('"') || s.startsWith("'") ? scalar(s) : s);
}
// Identify complete TOML statements without interpreting unrelated values.
// Multiline strings, arrays and comments remain byte-for-byte intact.
function statements(text) {
  const lines = text.replace(/^\uFEFF/, '').split(/\r?\n/);
  const result = []; let section = [], quote = '', depth = 0, start = 0;
  for (let row = 0; row < lines.length; row++) {
    const line = lines[row];
    if (!quote && depth === 0) {
      start = row;
      const header = line.match(/^\s*\[([^\[\]]+)\]\s*(?:#.*)?$/);
      if (header) { section = tablePath(header[1]); result.push({ start:row, end:row+1, section:[...section], header:true }); continue; }
      const arrayHeader = line.match(/^\s*\[\[(.+)\]\]\s*(?:#.*)?$/);
      if (arrayHeader) { section = ['@array', ...tablePath(arrayHeader[1])]; result.push({ start:row, end:row+1, section:[...section], header:true }); continue; }
    }
    for (let i = 0; i < line.length; i++) {
      const ch = line[i];
      if (quote === '"""' || quote === "'''") {
        if (quote === '"""' && ch === '\\') { i++; continue; }
        if (line.slice(i, i + 3) === quote) { quote = ''; i += 2; }
      } else if (quote) {
        if (quote === '"' && ch === '\\') { i++; continue; }
        if (ch === quote) quote = '';
      } else {
        if (ch === '#') break;
        if (ch === '"' || ch === "'") {
          quote = line.slice(i, i+3) === ch.repeat(3) ? ch.repeat(3) : ch;
          if (quote.length === 3) i += 2;
        } else if (ch === '[' || ch === '{') depth++;
        else if (ch === ']' || ch === '}') depth--;
      }
    }
    if (!quote && depth === 0) {
      const body = lines.slice(start, row+1).join('\n');
      const assignment = body.match(/^\s*([A-Za-z0-9_-]+|"[^"\n]+"|'[^'\n]+')\s*=([\s\S]*)$/);
      result.push({ start, end:row+1, section:[...section], key:assignment ? assignment[1].replace(/^["']|["']$/g, '') : undefined, value:assignment?.[2], body });
    }
    if (depth < 0) throw new Error('主应用配置结构不完整，原配置未修改');
  }
  if (quote || depth) throw new Error('主应用配置结构不完整，原配置未修改');
  return { lines, statements:result };
}
function featurePaths(profile) {
  const paths = [['features']];
  if (profile) paths.push(['profiles', profile, 'features']);
  return paths;
}
function sameSection(a, b) { return a.length === b.length && a.every((v, i) => v === b[i]); }
function withRequestCompressionOff(text, profile) {
  const doc = statements(text), paths = featurePaths(profile), deleted = new Set();
  for (const item of doc.statements) {
    if (item.key === FEATURE_FLAG && paths.some(p => sameSection(p, item.section))) {
      for (let i = item.start; i < item.end; i++) deleted.add(i);
    }
  }
  const headers = paths.map(p => doc.statements.find(s => s.header && sameSection(p, s.section))).filter(Boolean);
  const headerStarts = new Set(headers.map(s => s.start));
  const body = doc.lines.flatMap((line, i) => {
    if (deleted.has(i)) return [];
    if (headerStarts.has(i)) return [line, FEATURE_FLAG + ' = false'];
    return [line];
  }).join('\n').trim();
  const rootMissing = !doc.statements.some(s => s.header && sameSection(s.section, ['features']));
  return (body + (rootMissing ? '\n\n[features]\n' + FEATURE_FLAG + ' = false' : '')).trim() + '\n';
}
function restoreRequestCompression(text, original, profile) {
  const cur = statements(text), src = statements(original), paths = featurePaths(profile);
  const deleted = new Set(), values = new Map(), append = [];
  for (const p of paths) {
    const srcHeader = src.statements.find(s => s.header && sameSection(p, s.section));
    const srcKey = src.statements.find(s => s.key === FEATURE_FLAG && sameSection(p, s.section));
    const curHeader = cur.statements.find(s => s.header && sameSection(p, s.section));
    for (const item of cur.statements) {
      if (item.key === FEATURE_FLAG && sameSection(p, item.section)) {
        for (let i = item.start; i < item.end; i++) deleted.add(i);
      }
    }
    if (srcKey) {
      if (curHeader) values.set(curHeader.start, srcKey.value.trim());
      else if (p.length === 1) append.push(srcKey.value.trim());
    } else if (!srcHeader && curHeader) {
      const next = cur.statements.find(s => s.header && s.start > curHeader.start);
      const end = next ? next.start : cur.lines.length;
      let content = false;
      for (let i = curHeader.start + 1; i < end; i++) {
        if (deleted.has(i)) continue;
        const line = cur.lines[i];
        if (line.trim() && !line.trim().startsWith('#')) { content = true; break; }
      }
      if (!content) for (let i = curHeader.start; i < end; i++) deleted.add(i);
    }
  }
  const body = cur.lines.flatMap((line, i) => {
    if (deleted.has(i)) return [];
    if (values.has(i)) return [line, FEATURE_FLAG + ' = ' + values.get(i)];
    return [line];
  }).join('\n').trim();
  return (body + (append.length ? '\n\n[features]\n' + FEATURE_FLAG + ' = ' + append[0] : '')).trim() + '\n';
}
function mergeConfig(original, prepared, home, key, accountID, channel) {
  const parsed = statements(original), source = statements(prepared);
  const root = parsed.statements.filter(s => s.section.length === 0 && s.key);
  const profileEntry = root.find(s => s.key === 'profile');
  const profile = profileEntry && scalar(profileEntry.value);
  if (profileEntry && !profile) throw new Error('无法安全识别当前配置 profile');
  const activeProvider = parsed.statements.find(s => profile && s.section.length === 2 && s.section[0] === 'profiles' && s.section[1] === profile && s.key === 'model_provider') || root.find(s => s.key === 'model_provider');
  const provider = activeProvider ? scalar(activeProvider.value) : 'openai';
  if (!provider || !/^[A-Za-z0-9_-]{1,80}$/.test(provider)) throw new Error('当前供应商名称无法安全保留，原配置未修改');
  if (['ollama','lmstudio','amazon-bedrock','amazon-bedrock-runtime'].includes(provider)) throw new Error('当前主应用使用其他内置供应商，请先切回 OpenAI 或选择独立实例；原配置未修改');
  if (root.some(s => s.key === 'model_providers') || parsed.statements.some(s => s.section.length === 0 && /^\s*model_providers\./.test(s.body || ''))) throw new Error('请先将内联供应商配置改为标准 TOML 表，原配置未修改');
  const values = Object.fromEntries(source.statements.filter(s => !s.section.length && MANAGED.includes(s.key)).map(s => [s.key, s.value.trim()]));
  Object.assign(values, {model_provider:JSON.stringify(provider), model_catalog_json:JSON.stringify(path.join(home,'wishtoken-models.json')), cli_auth_credentials_store:'"file"'});
  if (provider === 'openai') {
    const base = source.statements.find(s => s.section[0] === 'model_providers' && s.section[1] === 'gptbridge' && s.key === 'base_url');
    if (!base || !/^acc-[A-Za-z0-9-]+$/.test(accountID || '') || !['bps','codex','bps_native','work'].includes(channel)) throw new Error('主应用本地通道信息不完整');
    const url = new URL(scalar(base.value));
    if (url.protocol !== 'http:' || !['127.0.0.1','localhost','[::1]'].includes(url.hostname)) throw new Error('主应用模型入口必须是本地服务');
    const scope = createHmac('sha256',key).update('desktop-api\n'+accountID+'\n'+channel).digest('hex');
    values.openai_base_url = JSON.stringify(url.origin+'/desktop-api/'+scope+'/'+accountID+'/'+channel+'/v1');
  }
  const deleted = new Set();
  for (const item of parsed.statements) {
    const ownProvider = item.section[0] === 'model_providers' && item.section[1] === provider;
    const top = item.section.length === 0;
    const activeProfile = profile && item.section.length === 2 && item.section[0] === 'profiles' && item.section[1] === profile;
    const tier = item.section.length === 1 && item.section[0] === 'desktop' && item.key === 'default-service-tier';
    if (ownProvider || ((top || activeProfile) && (MANAGED.includes(item.key) || ['forced_login_method','forced_chatgpt_workspace_id'].includes(item.key))) || tier) {
      for (let i=item.start;i<item.end;i++) deleted.add(i);
    }
  }
  const desktopTier = 'default-service-tier = ' + (values.service_tier || '"default"');
  let desktopFound = false;
  const kept = parsed.lines.flatMap((line,i)=>{
    if (deleted.has(i)) return [];
    const header=parsed.statements.find(s=>s.start===i && s.header && s.section.length===1 && s.section[0]==='desktop');
    if (header) { desktopFound=true; return [line,desktopTier]; }
    return [line];
  }).join('\n').trim();
  const providerLines = [];
  for (const item of source.statements) {
    if (item.section[0] !== 'model_providers' || item.section[1] !== 'gptbridge') continue;
    if (item.header) { providerLines.push('[model_providers.' + provider + item.section.slice(2).map(s => '.' + JSON.stringify(s)).join('') + ']'); continue; }
    if (item.key === 'env_key') providerLines.push('experimental_bearer_token = ' + JSON.stringify(key));
    else providerLines.push(...source.lines.slice(item.start,item.end));
  }
  if (!providerLines.length) throw new Error('缺少本地服务供应商配置');
  const top = Object.entries(values).map(([k,v])=>k+' = '+v).join('\n');
  const merged = (top + '\n\n' + kept + '\n\n' + (provider === 'openai' ? '' : providerLines.join('\n').trim() + '\n') + (desktopFound ? '' : '\n[desktop]\n'+desktopTier+'\n')).trim();
  return { provider, text: withRequestCompressionOff(merged, profile) };
}
function read(file) { try { return fs.readFileSync(file); } catch(e) { if(e.code==='ENOENT') return null; throw e; } }
function hash(value) { return value === null ? null : createHash('sha256').update(value).digest('hex'); }
function atomic(file, raw) {
  if (raw === null) { fs.rmSync(file,{force:true}); return; }
  const temporary = file + '.wishtoken-' + randomUUID() + '.tmp';
  try { fs.writeFileSync(temporary,raw,{mode:0o600}); fs.renameSync(temporary,file); } finally { fs.rmSync(temporary,{force:true}); }
}
function markerFile(dataHome) { return path.join(dataHome,'main-profile.json'); }
function status(dataHome, home) {
  const raw=read(markerFile(dataHome));
  if (!raw) return { active:false, home };
  const state=JSON.parse(raw.toString());
  if (path.resolve(state.home)!==path.resolve(home)) return { active:false, home };
  return {active:true,home,backup:state.backup,account_id:state.account_id,channel:state.channel};
}
function prepare({dataHome,home,profileHome,key,accountID,channel}) {
  home=path.resolve(home); profileHome=path.resolve(profileHome);
  if (home===profileHome) throw new Error('主应用和临时配置目录不能相同');
  const before=Object.fromEntries(FILES.map(name=>[name,read(path.join(home,name))]));
  const config=fs.readFileSync(path.join(profileHome,'config.toml'),'utf8');
  const merged=mergeConfig(before['config.toml']?.toString('utf8') || '',config,home,key,accountID,channel);
  const auth=read(path.join(profileHome,'auth.json'));
  const metadata=read(path.join(profileHome,'bridge-identity.json'));
  if (!auth || !metadata) throw new Error('该账号缺少主应用身份信息，请重新导入有效的子号 JSON');
  const after={'config.toml':Buffer.from(merged.text),'auth.json':auth,'bridge-identity.json':metadata,'wishtoken-models.json':fs.readFileSync(path.join(profileHome,'models.json'))};
  return {dataHome,home,before,after,provider:merged.provider,accountID,channel};
}
function apply(plan) {
  const {dataHome,home,before,after}=plan;
  for (const name of FILES) if(hash(read(path.join(home,name)))!==hash(before[name])) throw new Error('主应用配置刚刚发生变化，请重新点击切换；尚未覆盖任何文件');
  fs.mkdirSync(home,{recursive:true,mode:0o700});
  const marker=markerFile(dataHome), oldMarker=read(marker);
  const prior=oldMarker && JSON.parse(oldMarker.toString());
  if (prior && path.resolve(prior.home)!==home) throw new Error('已有其他主应用目录的备份，请先恢复后再更改目录');
  let backup=prior?.backup;
  if (!backup) {
    backup=path.join(dataHome,'main-app-backups',new Date().toISOString().replace(/[:.]/g,'-')+'-'+randomUUID());
    fs.mkdirSync(backup,{recursive:true,mode:0o700});
    for(const name of FILES) if(before[name]!==null) fs.writeFileSync(path.join(backup,name),before[name],{mode:0o600});
    fs.writeFileSync(path.join(backup,'manifest.json'),JSON.stringify({home,files:Object.fromEntries(FILES.map(n=>[n,before[n]!==null]))}),{mode:0o600});
  }
  const written=[];
  try {
    for(const name of FILES){atomic(path.join(home,name),after[name]);written.push(name);}
    atomic(marker,Buffer.from(JSON.stringify({home,backup,account_id:plan.accountID,channel:plan.channel,hashes:Object.fromEntries(FILES.map(n=>[n,hash(after[n])]))},null,2)));
  } catch(error) {
    for(const name of written.reverse()) if(hash(read(path.join(home,name)))===hash(after[name])) atomic(path.join(home,name),before[name]);
    throw new Error('主应用配置写入失败，已尝试回滚；备份位于 '+backup+'：'+error.message);
  }
  return {home,backup,provider:plan.provider,rollback:()=>{
    for(const name of FILES) if(hash(read(path.join(home,name)))!==hash(after[name])) throw new Error('配置已被其他程序修改，请从备份手动恢复：'+backup);
    for(const name of FILES) atomic(path.join(home,name),before[name]);
    atomic(marker,oldMarker);
  }};
}
// Restore only the keys and provider table WishToken manages, keeping projects,
// plugins, MCP servers and other settings changed after the takeover.
function restoreManagedConfig(original, current) {
  const src=statements(original), dst=statements(current);
  const root=src.statements.filter(s=>s.section.length===0 && s.key);
  const providerEntry=root.find(s=>s.key==='model_provider');
  const provider=providerEntry?scalar(providerEntry.value):null;
  if(providerEntry && !provider) throw new Error('备份中的供应商名称无法识别');
  const managed=new Set([...MANAGED,'forced_login_method','forced_chatgpt_workspace_id']);
  const values=root.filter(s=>managed.has(s.key)).map(s=>({key:s.key,value:s.value.trim()}));
  const desktopTier=src.statements.find(s=>s.section.length===1 && s.section[0]==='desktop' && s.key==='default-service-tier');
  const providerLines=[];
  if(provider) for(const s of src.statements){ if(s.section[0]==='model_providers' && s.section[1]===provider) providerLines.push(...src.lines.slice(s.start,s.end)); }
  const profileEntry=dst.statements.find(s=>s.section.length===0 && s.key==='profile');
  const profile=profileEntry && scalar(profileEntry.value);
  const originalProfileEntry=src.statements.find(s=>s.section.length===0 && s.key==='profile');
  const originalProfile=originalProfileEntry && scalar(originalProfileEntry.value);
  const profileLines=profile && profile===originalProfile ? src.statements.filter(s=>s.section.length===2 && s.section[0]==='profiles' && s.section[1]===profile && s.key && managed.has(s.key)).map(s=>s.body.trim()) : [];
  const deleted=new Set();
  for(const s of dst.statements){
    const top=s.section.length===0;
    const activeProfile=profile && s.section.length===2 && s.section[0]==='profiles' && s.section[1]===profile;
    const ownProvider=provider && s.section[0]==='model_providers' && s.section[1]===provider;
    const tier=s.section.length===1 && s.section[0]==='desktop' && s.key==='default-service-tier';
    if(((top||activeProfile)&&s.key&&managed.has(s.key)) || ownProvider || tier) for(let i=s.start;i<s.end;i++)deleted.add(i);
  }
  const desktopLine=desktopTier?desktopTier.body.trim():null;
  let desktopFound=false;
  const kept=dst.lines.flatMap((line,i)=>{
    if(deleted.has(i)) return [];
    const profileHeader=dst.statements.find(s=>s.start===i && s.header && s.section.length===2 && s.section[0]==='profiles' && s.section[1]===profile);
    if(profileHeader && profileLines.length) return [line,...profileLines];
    const header=dst.statements.find(s=>s.start===i && s.header && s.section.length===1 && s.section[0]==='desktop');
    if(header){ desktopFound=true; return desktopLine?[line,desktopLine]:[line]; }
    return [line];
  }).join('\n').trim();
  const top=values.map(v=>v.key+' = '+v.value).join('\n');
  const restored=(top+'\n\n'+kept+'\n\n'+(providerLines.length?providerLines.join('\n').trim()+'\n':'')+(!desktopFound&&desktopLine?'\n[desktop]\n'+desktopLine+'\n':'')).trim()+'\n';
  return restoreRequestCompression(restored, original, profile);
}

function restoreManaged(dataHome,home) {
  home=path.resolve(home);
  const marker=markerFile(dataHome), raw=read(marker);
  if(!raw) throw new Error('没有可恢复的 Codex 接管记录');
  const state=JSON.parse(raw.toString());
  if(path.resolve(state.home)!==home) throw new Error('备份与当前主应用目录不一致');
  // With no external edits, restore the original bytes, including table order
  // and whitespace. Merge only when preserving later user/application edits.
  if(FILES.every(name=>hash(read(path.join(home,name)))===state.hashes[name])) return restore(dataHome,home);
  const original=read(path.join(state.backup,'config.toml'));
  if(!original) throw new Error('接管前的 config.toml 备份不存在');
  const saved=JSON.parse(fs.readFileSync(path.join(state.backup,'manifest.json'),'utf8'));
  const current=read(path.join(home,'config.toml'));
  if(!current) throw new Error('主应用 config.toml 不存在');
  const merged=restoreManagedConfig(original.toString('utf8'),current.toString('utf8'));
  const safety=path.join(dataHome,'main-app-backups','restore-'+new Date().toISOString().replace(/[:.]/g,'-')+'-'+randomUUID());
  fs.mkdirSync(safety,{recursive:true,mode:0o700});
  const before=Object.fromEntries(FILES.map(name=>[name,read(path.join(home,name))]));
  for(const name of FILES) if(before[name]!==null) fs.writeFileSync(path.join(safety,name),before[name],{mode:0o600});
  const written=[];
  try {
    atomic(path.join(home,'config.toml'),Buffer.from(merged)); written.push('config.toml');
    for(const name of ['wishtoken-models.json','bridge-identity.json']) {
      if(!saved.files?.[name]){ atomic(path.join(home,name),null); written.push(name); }
      else { const value=read(path.join(state.backup,name)); if(value!==null){ atomic(path.join(home,name),value); written.push(name); } }
    }
    // Keep native ChatGPT tokens refreshed by Codex; only restore a past
    // backup when the current auth.json is WishToken's credential projection.
    const auth=read(path.join(home,'auth.json'));
    let projection=false;
    try { const doc=JSON.parse(auth.toString()); projection=!doc.tokens && ('personal_access_token' in doc || 'OPENAI_API_KEY' in doc); } catch {}
    if(projection && saved.files?.['auth.json']){ const value=read(path.join(state.backup,'auth.json')); if(value!==null){ atomic(path.join(home,'auth.json'),value); written.push('auth.json'); } }
    atomic(marker,null);
  } catch(error) {
    for(const name of written.reverse()) atomic(path.join(home,name),before[name]);
    throw error;
  }
  return {home,backup:state.backup,safety,restored:true};
}

function checkRestore(dataHome,home) {
  const marker=markerFile(dataHome), raw=read(marker);
  if(!raw) throw new Error('没有可恢复的主应用接管备份');
  const state=JSON.parse(raw.toString());
  if(path.resolve(state.home)!==path.resolve(home)) throw new Error('备份与当前主应用目录不一致');
  for(const name of FILES) if(hash(read(path.join(home,name)))!==state.hashes[name]) throw new Error('接管后配置已被其他程序修改，为避免覆盖新设置，请从备份手动恢复：'+state.backup);
  const saved=JSON.parse(fs.readFileSync(path.join(state.backup,'manifest.json'),'utf8'));
  for (const name of FILES) if (saved.files[name]) fs.accessSync(path.join(state.backup,name),fs.constants.R_OK);
  return {state,saved};
}
function restore(dataHome,home) {
  const {state,saved}=checkRestore(dataHome,home);
  const marker=markerFile(dataHome);
  const current=Object.fromEntries(FILES.map(n=>[n,read(path.join(home,n))]));
  const restored=[];
  try{
    for(const name of FILES){atomic(path.join(home,name),saved.files[name]?fs.readFileSync(path.join(state.backup,name)):null);restored.push(name);}
    atomic(marker,null);
  }catch(error){for(const name of restored.reverse())atomic(path.join(home,name),current[name]);throw error;}
  return {home,backup:state.backup,restored:true};
}
module.exports={mainHome,mergeConfig,prepare,apply,restore,restoreManaged,restoreManagedConfig,checkRestore,status};
