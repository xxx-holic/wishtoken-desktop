'use strict';
// Owner-side utility. Never package the issuer private key with the application.
const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const [command,...args]=process.argv.slice(2);const opts={};
for(let i=0;i<args.length;i+=2){if(!args[i]?.startsWith('--')||!args[i+1]||opts[args[i]])throw Error('Use --name value pairs');opts[args[i]]=args[i+1];}
function required(name){if(!opts[name])throw Error('Missing '+name);return opts[name];}
function writeNew(file,data){fs.mkdirSync(path.dirname(path.resolve(file)),{recursive:true,mode:0o700});fs.writeFileSync(file,data,{flag:'wx',mode:0o600});}
if(command==='init'){
 const privateFile=required('--private-key'),publicFile=required('--public-key');
 if(fs.existsSync(privateFile))throw Error('Private key exists; never replace an active issuer');
 const pair=crypto.generateKeyPairSync('ed25519');const pub=pair.publicKey.export({format:'der',type:'spki'}).subarray(-32).toString('base64');
 if(fs.existsSync(publicFile)&&fs.readFileSync(publicFile,'utf8').trim()!=='KEY_PENDING_LOCAL_ISSUER_INITIALIZATION')throw Error('Public key already configured');
 writeNew(privateFile,pair.privateKey.export({format:'pem',type:'pkcs8'}));
 fs.mkdirSync(path.dirname(path.resolve(publicFile)),{recursive:true});fs.writeFileSync(publicFile,pub+'\n','utf8');
 console.log(JSON.stringify({initialized:true,privateKeyPath:path.resolve(privateFile),publicKeyPath:path.resolve(publicFile)}));
}else if(command==='issue'){
 const installation=required('--installation'),days=Number(required('--days')),out=required('--out');
 if(!/^WT-[A-F0-9]{32}$/.test(installation))throw Error('Invalid installation identifier');
 if(!Number.isSafeInteger(days)||days<1||days>3650)throw Error('Days must be between 1 and 3650');
 const key=crypto.createPrivateKey(fs.readFileSync(required('--private-key')));
 if(key.asymmetricKeyType!=='ed25519')throw Error('Expected an Ed25519 key');
 const now=Math.floor(Date.now()/1000),claims={v:1,product:'wishtoken-desktop',feature:'fenjue',installation,id:crypto.randomUUID(),issued_at:now,expires_at:now+days*86400};
 const signed='WTK1.'+Buffer.from(JSON.stringify(claims)).toString('base64url');const sig=crypto.sign(null,Buffer.from(signed),key);
 if(!crypto.verify(null,Buffer.from(signed),crypto.createPublicKey(key),sig))throw Error('Issuer self-check failed');
 writeNew(out,signed+'.'+sig.toString('base64url')+'\n');
 console.log(JSON.stringify({issued:true,installation,expiresAt:new Date(claims.expires_at*1000).toISOString(),output:path.resolve(out)}));
}else throw Error('Use init --private-key PATH --public-key PATH, or issue --private-key PATH --installation WT-... --days N --out PATH');
