// 仅检查本地图纸阅读器的字号与缩放，不访问业务接口。
async (page) => {
  const origin='http://127.0.0.1:8766';
  await page.setViewportSize({width:1440,height:1000});
  await page.goto(`${origin}/previews/index.html#p11`);
  await page.evaluate(()=>document.fonts.ready);
  await page.waitForTimeout(100);
  const deepLink=await page.evaluate(()=>({scrollY:window.scrollY,titleTop:document.querySelector('.sheet.active h2').getBoundingClientRect().top,
    headerBottom:document.querySelector('header').getBoundingClientRect().bottom}));
  if(deepLink.scrollY!==0||deepLink.titleTop<deepLink.headerBottom)throw new Error('深链接标题被固定页头遮住');
  const dimensions=()=>page.evaluate(()=>{
    const drawing=document.querySelector('.sheet.active .drawing'),svg=drawing.querySelector('svg');
    return {zoom:document.querySelector('#zoom').value,originalWidth:Number(drawing.dataset.width),width:svg.getBoundingClientRect().width,
      originalHeight:Number(drawing.dataset.height),height:svg.getBoundingClientRect().height,availableWidth:drawing.clientWidth,availableHeight:drawing.clientHeight};
  });
  const normal=await dimensions();
  if(normal.zoom!=='1'||Math.abs(normal.width-normal.originalWidth)>1)throw new Error('100%未保持原生尺寸');
  await page.screenshot({path:'D:/project/yuejuan/output/playwright/font-default-100.png'});
  await page.selectOption('#zoom','1.5');
  await page.selectOption('#picker','p19');
  const enlarged=await dimensions();
  if(enlarged.zoom!=='1.5'||Math.abs(enlarged.width-enlarged.originalWidth*1.5)>1)throw new Error('换页未保留150%缩放');
  await page.selectOption('#zoom','fit');
  const fitted=await dimensions();
  if(fitted.width>fitted.availableWidth+1||fitted.height>fitted.availableHeight+1)throw new Error('适应页面超出阅读区域');
  await page.goto(`${origin}/previews/index.html?contact=1&start=54&count=9`);
  const contact=await page.evaluate(()=>({
    count:[...document.querySelectorAll('.sheet')].filter(s=>getComputedStyle(s).display!=='none').length,
    headerVisible:getComputedStyle(document.querySelector('header')).display!=='none',
    heights:[...document.querySelectorAll('.sheet')].filter(s=>getComputedStyle(s).display!=='none').map(s=>s.querySelector('svg').getBoundingClientRect().height)
  }));
  if(contact.count!==9||contact.headerVisible||contact.heights.some(h=>Math.abs(h-400)>1))throw new Error('联系表布局改变');
  const report={checked_at_utc:new Date().toISOString(),passed:true,deepLink,default100:normal,keep150AcrossPages:enlarged,fit:fitted,contact};
  const saved=await page.request.post(`${origin}/evidence/preview-typography-validation.json`,{data:JSON.stringify(report,null,2),headers:{'Content-Type':'application/json'}});
  if(!saved.ok())throw new Error('预览检查记录写入失败');
  return {passed:true,default100:true,keep150AcrossPages:true,fit:true,contactPages:contact.count};
}
