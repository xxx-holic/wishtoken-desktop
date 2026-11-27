'use strict';
const PELICAN_PROMPT = '创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，你不需要任何测试';
const CANDY_PROMPT = '在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）\n苹果味 桃子味 西瓜味\n圆形 7 9 8\n五角星形 7 6 4';
function candyVerdict(text, prompt = CANDY_PROMPT) {
  if (prompt !== CANDY_PROMPT) return { verdict:'review', answer:null, reason:'题目已修改，请人工判断' };
  const plain = text.replace(/^\s*#{1,6}\s+/gm,'').replace(/\*|`|\$/g,'').replace(/\\[()[\]]/g,'').replace(/\\(?:boxed|mathbf|textbf)\{(\d+)\}/g,'$1');
  const conclusions = [...plain.matchAll(/(?:最终答案|答案|最少(?:需要|要|应)?(?:取出|摸出|取|摸)?|至少(?:需要|要)?(?:取出|摸出|取|摸)?)(?:是|为|应为|需要|应取|要取|取出|摸出|取|摸|\s|[:：=])*([0-9]{1,3})(?:\s*(?:颗|个|粒)|\b)/g)]
    .filter(match => !/^\s*(?:的)?(?:圆形|五角星形?|星形)/.test(plain.slice(match.index + match[0].length)) && !/(?:圆形|五角星形?|星形)\s*$/.test(plain.slice(0,match.index)));
  if (new Set(conclusions.map(match=>Number(match[1]))).size > 1) return { verdict:'review', answer:null, reason:'回答包含多个条件下的结论，请核对原始回答' };
  let answer = conclusions.length ? Number(conclusions.at(-1)[1]) : null;
  if (answer === null && /^\s*\d{1,3}\s*(?:颗|个|粒)?[。.!！]?\s*$/.test(plain)) answer=Number(plain.match(/\d+/)[0]);
  if (answer === null) return { verdict:'review', answer:null, reason:'未识别到明确结论，请查看原始回答' };
  return { verdict:answer===21 ? 'pass' : 'fail', answer, reason:answer===21 ? '识别到结论 21，符合本题预期' : '识别到结论 '+answer+'，与本题预期 21 不符' };
}
module.exports={ PELICAN_PROMPT, CANDY_PROMPT, candyVerdict };
