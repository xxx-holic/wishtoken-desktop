'use strict';
const test=require('node:test'),assert=require('node:assert/strict');
const {requireFenjue,licenseCode}=require('../lib/feature-access.cjs');
test('feature gate fails closed and permits only explicit verified activation',async()=>{
 for(const status of [{},{unlocked:false,reason:'expired'},{unlocked:'true'}])await assert.rejects(()=>requireFenjue({request:async()=>status}),/锁定/);
 const active={unlocked:true,expires_at:123};assert.equal(await requireFenjue({request:async()=>active}),active);
 await assert.rejects(()=>requireFenjue({request:async()=>{throw Error('offline')}}),/offline/);
});
test('feature code input is bounded, not a plain toggle',()=>{assert.equal(licenseCode(' WTK1.abc.def '),'WTK1.abc.def');for(const x of [null,true,'enabled','WTK1.a.b.extra','x'.repeat(9000)])assert.throws(()=>licenseCode(x));});
