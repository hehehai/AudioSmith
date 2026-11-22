package voicedesign

// The parser's lexicon, one rule per line:
//
//	<assignments>: phrase|phrase|…
//
// Assignments are comma-separated `category=value` (strong) or
// `category~value` (weak: applies only when no strong phrase set the
// category — "boy" says male for sure but a child only by default).
// Special left sides: `!stop` (filler, neither mapped nor reported),
// `!neg` (negates the next mapped phrase), `!int` (intensifier: "very" +
// low → very_low).
//
// English phrases are lower case, words separated by single spaces
// (hyphens and underscores in the input count as spaces); a trailing `*`
// matches any word ending ("whisper*" → whispering, whispery). Chinese
// phrases match anywhere; at one position the longest phrase wins. The
// Chinese table also has a few Japanese words for age and gender (女の子,
// 七十代: Irodori-TTS's captions, curated Japanese voices, thread #172).
const lexiconEN = `
gender=male: male|man|men|guy|guys|gentleman|gentlemen|gent|masculine|dude|fellow|sir|lad|husband|king|baritone voiced
gender=male,age~child: boy|boys|boyish
gender=male,age=child: little boy|young boy|schoolboy
gender=male,age~young_adult: young man|prince|son
gender=male,age~middle_aged: father|dad|uncle|middle aged man
gender=male,age=elderly: grandfather|grandpa|granddad|old man|elderly man|old gentleman|grumpy old man
gender~male: he|him|his|himself
gender=female: female|woman|women|lady|ladies|gal|feminine|wife|queen|madam
gender=female,age~teen: girl|girls|girly|girlish
gender=female,age=child: little girl|young girl|schoolgirl
gender=female,age~young_adult: young woman|young lady|princess|daughter
gender=female,age~middle_aged: mother|mom|mum|aunt|auntie|middle aged woman
gender=female,age=elderly: grandmother|grandma|granny|nana|old woman|old lady|elderly woman
gender~female: she|her|hers|herself
age=child: child|children|childlike|kid|kids|kiddo|toddler|little one|preschooler
age=teen: high school|teen|teens|teenage|teenager|teenagers|teenaged|adolescent|youthful teen
age=young_adult: young|young adult|youthful|twenties|twentysomething|thirties|college student|student
age=middle_aged: middle aged|middle age|mature|forties|fifties|midlife
age=elderly: elderly|old|older|aged|aging|senior|seniors|elder|sixties|seventies|eighties|ancient|wizened|venerable
pitch=very_low: bass|basso|sub bass|very deep|extremely deep|cavernous
pitch=low: deep*|low|low pitched|low pitch|lower pitched|baritone|alto|contralto|low register|throaty
pitch=low,style=resonant: booming|boom*|sonorous
pitch=moderate: medium pitch|medium pitched|mid pitch|mid pitched|moderate pitch|normal pitch|average pitch|natural pitch|neutral pitch|tenor
pitch=high: high|high pitched|high pitch|higher pitched|soprano|light voiced|piping
pitch=very_high: squeaky|squeak*|shrill|very high pitched|chipmunk|falsetto
pace=very_slow: glacial|very slow|extremely slow|painfully slow
pace=slow: slow*|unhurried|leisurely|measured|deliberate|deliberately|drawl*|languid|lingering|takes his time|takes her time|takes their time
pace=moderate: moderate pace|normal pace|steady pace|natural pace|medium pace|average pace|conversational pace
pace=fast: fast*|quick*|rapid|rapidly|brisk*|hurried|speedy|fast talking|fast paced|swift*|snappy|breathless
pace=very_fast: rapid fire|machine gun|very fast|extremely fast|auctioneer
style=whisper: whisper*|hushed|murmur*|sotto voce|under her breath|under his breath
style=breathy: breathy|airy|breathier|wispy
style=raspy: raspy|rasp*|gravel*|husky|huskier|hoarse|rough|gritty|scratchy|smoky|smoker's|craggy|weathered|grizzled|whiskey soaked
style=nasal: nasal|nasally|twang*|nasal sounding
style=smooth: smooth*|silky|velvet*|mellow|buttery|honeyed|creamy|liquid
style=warm: warm*|cozy|cosy|comforting|heartfelt|kindly
style=bright: bright*|sparkling|sparkly|radiant|sunny|chipper
style=crisp: crisp*|clear|clearly|articulate|precise|clean|enunciated|well spoken
style=soft: soft*|gentle|gently|soothing|delicate|quiet|quietly|hushed tone|lullaby
style=resonant: resonant|rich|full bodied|robust|hearty|powerful voice|commanding presence
style=magnetic: magnetic|charismatic|alluring|captivating|seductive|sultry
style=sweet: sweet*|cute|adorable|sugary|honey sweet
style=energetic: energetic|energized|lively|upbeat|peppy|bubbly|dynamic|vibrant|high energy|animated|spirited|zesty|enthusiastic energy|hype|hyped|pumped
style=calm: calm*|relaxed|serene|steady|composed|laid back|low key|low energy|tranquil|peaceful|placid|unflappable|zen|meditative
style=authoritative: authoritative|commanding|confident|assertive|powerful|strong|firm|stern authority|in charge|decisive|bold
style=friendly: friendly|approachable|welcoming|amiable|affable|personable|neighborly|neighbourly|easygoing|casual|conversational
style=playful: playful|mischievous|cheeky|whimsical|quirky|impish|goofy|silly|teasing|cartoonish|cartoony|zany
style=dramatic: dramatic|theatrical|epic|intense|expressive|grandiose|over the top|cinematic|trailer
style=professional: professional|polished|corporate|formal|businesslike|business like|refined|sophisticated|newsreader|broadcast
emotion=neutral: neutral|flat|matter of fact|monotone|deadpan|even toned|impartial
emotion=happy: happy|joyful|joyous|glad|cheerful|cheery|jolly|merry|delighted|smiling|gleeful|jovial
emotion=sad: sad|sadly|melancholy|melancholic|sorrowful|gloomy|tearful|mournful|wistful|heartbroken|somber|sombre|downcast|grieving
emotion=angry: angry|furious|irate|enraged|irritated|annoyed|mad|livid|seething|snarling|aggressive|fierce|hostile
emotion=excited: excited|thrilled|enthusiastic|ecstatic|eager|elated|exhilarated|overjoyed|hyper
emotion=surprised: surprised|astonished|shocked|amazed|astounded|stunned|startled
emotion=fearful: fearful|scared|afraid|frightened|terrified|nervous|anxious|timid|trembling|panicked|worried
emotion=tender: tender|loving|affectionate|caring|compassionate|nurturing|sweetly|romantic|intimate
emotion=serious: serious|stern|solemn|grave|earnest|sober|severe|no nonsense|businesslike tone
emotion=mysterious: mysterious|eerie|enigmatic|ominous|haunting|cryptic|spooky|sinister|creepy|menacing
accent=american: american|general american|usa|u s|midwestern|new york|new yorker|californian|yankee
accent=british: british|brit|uk|english accent|received pronunciation|rp|posh|london|londoner|cockney|oxford|queen's english|estuary
accent=australian: australian|aussie|ozzie|australia
accent=canadian: canadian|canada
accent=indian: indian|india|hinglish|indian english
accent=chinese: chinese accent|chinese accented|chinese english|chinglish
accent~chinese: chinese
accent=korean: korean|korea
accent=japanese: japanese|japan
accent=portuguese: portuguese|brazilian|portugal|brazil
accent=russian: russian|russia|slavic
accent=irish: irish|ireland|dublin
accent=scottish: scottish|scots|scotland|glaswegian
accent=new_zealand: new zealand|kiwi|nz
accent=south_african: south african|south africa
accent=southern_us,pace~slow: southern drawl|texas drawl
accent=southern_us: southern|southern american|texan|texas|deep south|country accent|appalachian
accent=french: french|france|parisian
accent=german: german|germany|bavarian|austrian
accent=spanish: spanish|spain|mexican|latino|latina|hispanic
dialect=sichuan: sichuan|sichuanese|szechuan|szechwan|chengdu|chongqing
dialect=northeastern: northeastern|north eastern|dongbei|manchurian|harbin|shenyang
dialect=henan: henan|zhengzhou
dialect=shaanxi: shaanxi|xi'an|xian|shaanxinese
dialect=guizhou: guizhou|guiyang
dialect=yunnan: yunnan|kunming
dialect=guilin: guilin|guangxi
dialect=jinan: jinan|shandong
dialect=qingdao: qingdao
dialect=shijiazhuang: shijiazhuang|hebei
dialect=gansu: gansu|lanzhou
dialect=ningxia: ningxia|yinchuan
dialect=beijing: beijing|beijinger|pekingese|peking|beijing accent
dialect=tianjin: tianjin
dialect=cantonese: cantonese|cantonese accented|hong kong|guangdong|guangzhou|cantonese mandarin
dialect=taiwanese: taiwanese|taiwan|taipei|taiwanese mandarin
dialect=shanghai: shanghai|shanghainese
!neg: not|no|non|never|without|isn't|isnt|aren't|arent|wasn't|don't|dont|doesn't|doesnt|nor|lacking|neither|less
!int: very|extremely|super|incredibly|exceptionally|ultra|insanely
!stop: really|so|too|truly|a|an|the|with|and|or|but|of|in|on|at|to|for|from|by|as|is|are|be|was|were|it|its|it's|that|this|these|those|who|whom|which|whose|voice|voices|voiced|speaker|speakers|speaking|speaks|speak|spoken|sound|sounds|sounding|like|tone|tones|toned|timbre|quality|person|people|someone|somebody|character|slightly|slight|bit|little|somewhat|rather|quite|fairly|kind|sort|touch|hint|hints|lot|pretty|just|has|have|having|had|talks|talking|talk|speech|delivery|accent|accented|accents|english|mandarin|dialect|pitch|pitched|pace|paced|tempo|style|mood|feel|feeling|vibe|vibes|way|manner|also|maybe|perhaps|mostly|mainly|generally|always|often|sometimes|i|me|my|we|our|you|your|they|them|their|want|wants|need|needs|would|could|should|will|can|make|makes|made|give|please|something|bit of|lots|plenty|type|kinda|sorta|ish|almost|nearly|around|about|over|under|than|more|most|much|one|two|year|years|yo|age|plus|very much|chinese speaking|standard|native|while|when|reading|reads|read|narrating|narrates|says|said|saying|good|nice|great|natural|naturally|old fashioned|old school|good looking
`

const lexiconZH = `
gender=male: 男|男性|男人|男生|男士|男声|先生|男子|汉子|爷们|帅哥
gender=male,age=child: 小男孩|男童|男孩子|男の子
gender=male,age~child: 男孩|小子
gender=male,age~young_adult: 小伙|小伙子|小哥|小哥哥|青年男性|年轻男人|男青年|大男孩|公子
gender=male,age=middle_aged: 大叔|中年男人|中年男性
gender=male,age~middle_aged: 叔叔|爸爸|父亲|老爸|大哥
gender=male,age=elderly: 爷爷|老爷爷|老头|老头儿|老大爷|大爷|老爷子|外公|老先生|老汉|おじいさん|おじいちゃん
gender=female: 女|女性|女人|女生|女士|女声|女子|美女|姐姐|小姐姐
gender=female,age=child: 小女孩|女童|女孩子|小姑娘|萝莉|女の子
gender=female,age~teen: 女孩|少女|女学生
gender=female,age~young_adult: 姑娘|小姐|年轻女人|青年女性|女青年|御姐
gender=female,age=middle_aged: 阿姨|大妈|中年女人|中年女性|大婶|大姐
gender=female,age~middle_aged: 妈妈|母亲|老妈|婶婶
gender=female,age=elderly: 奶奶|老奶奶|老太太|老太|外婆|姥姥|婆婆|老妇人|おばあさん|おばあちゃん|老婦人
gender~male: 他
gender~female: 她
age=child: 儿童|孩子|小孩|小孩子|童声|宝宝|小朋友|幼儿|稚嫩|子ども|子供
age=teen: 少年|青少年|中学生|高中生|初中生
age=young_adult: 青年|年轻|年轻人|大学生|二十多岁|三十多岁|二十代|三十代|若い
age=middle_aged: 中年|成熟|中年人|四十多岁|五十多岁|四十代|五十代
age=elderly: 老年|老人|老年人|年迈|年长|苍老|老者|老迈|上了年纪|六十多岁|七十多岁|八十多岁|六十代|七十代|八十代|年配|高齢|お年寄り
pitch=very_low: 极低|超低音|重低音
pitch=low: 低沉|低音|深沉|低|低音调|低嗓|沉
pitch=low,style=resonant: 浑厚低沉|雄浑|洪亮
pitch=moderate: 中音|音调适中|中音调|音高适中|音调正常
pitch=high: 高音|高亢|高音调|高|偏高|亮嗓
pitch=very_high: 尖细|尖锐|尖声|尖|极高|超高音|尖叫
pace=very_slow: 极慢|非常慢|特别慢
pace=slow: 慢|缓慢|慢悠悠|不紧不慢|慢条斯理|徐缓|慢吞吞|从容不迫|娓娓道来
pace=moderate: 语速适中|中速|正常语速|语速正常|不快不慢
pace=fast: 快|语速快|急促|飞快|快速|语速很快|麻利|利落|干脆利落
pace=very_fast: 连珠炮|机关枪|极快|语速极快
style=whisper: 耳语|低语|悄悄话|悄悄|轻声细语|私语|喃喃|呢喃
style=breathy: 气声|气音|气息感|空灵
style=raspy: 沙哑|嘶哑|粗糙|烟嗓|粗犷|沧桑|粗哑|哑|破锣嗓
style=nasal: 鼻音|鼻音重|囔囔
style=smooth: 顺滑|丝滑|圆润|柔滑|醇厚|丝绒
style=warm: 温暖|暖|温和|暖心|暖男
style=bright: 明亮|清亮|透亮|阳光|开朗
style=crisp: 清脆|清晰|干净|字正腔圆|吐字清晰|利索
style=soft: 柔和|轻柔|柔软|柔|软糯|轻声|舒缓|安抚
style=resonant: 浑厚|厚重|饱满|有力|中气十足|宏亮
style=magnetic: 磁性|有磁性|迷人|性感|魅力
style=sweet: 甜美|甜|软萌|萌|可爱|甜甜
style=energetic: 活力|有活力|充满活力|活泼|元气|朝气|热情|激情|动感|带劲
style=calm: 平静|沉稳|冷静|淡定|从容|安静|安详|稳重|稳|平和|宁静
style=authoritative: 权威|威严|自信|霸气|强势|威风|果断|坚定|有威严
style=friendly: 亲切|友好|亲和|和蔼|和善|随和|平易近人|接地气|聊天
style=playful: 俏皮|调皮|顽皮|搞怪|逗趣|滑稽|幽默|卡通
style=dramatic: 戏剧|戏剧化|夸张|激昂|磅礴|史诗|大气|电影感|预告片
style=professional: 专业|正式|商务|标准|播音腔|播音|新闻腔|字正
emotion=neutral: 中性|平淡|面无表情|没有情绪|客观|不带感情
emotion=happy: 开心|高兴|快乐|愉快|欢快|喜悦|愉悦|乐呵呵|笑眯眯|欢乐
emotion=sad: 悲伤|伤心|难过|忧伤|哀伤|低落|沮丧|凄凉|忧郁|哭腔
emotion=angry: 生气|愤怒|恼火|暴躁|怒气冲冲|发火|凶|凶狠|咆哮
emotion=excited: 兴奋|激动|亢奋|雀跃|热血
emotion=surprised: 惊讶|吃惊|震惊|惊奇|诧异|惊喜
emotion=fearful: 害怕|恐惧|紧张|胆怯|惊恐|胆小|颤抖|不安
emotion=tender: 温柔|深情|柔情|慈爱|疼爱|宠溺|体贴
emotion=serious: 严肃|庄重|冷峻|严厉|一本正经|郑重|肃穆
emotion=mysterious: 神秘|诡异|阴森|阴郁|阴沉|悬疑|恐怖|鬼魅
accent=american: 美式|美音|美国|美式英语|美国口音
accent=british: 英式|英音|英国|英伦|伦敦腔|伦敦|英式英语|英国口音
accent=australian: 澳洲|澳大利亚|澳式
accent=canadian: 加拿大
accent=indian: 印度|印式|印度英语
accent=chinese: 中式英语|中国口音|中式口音|中式
accent=korean: 韩国|韩式|韩语口音
accent=japanese: 日本|日式|日语口音
accent=portuguese: 葡萄牙|巴西
accent=russian: 俄罗斯|俄式|俄国|俄语口音
accent=irish: 爱尔兰
accent=scottish: 苏格兰
accent=new_zealand: 新西兰
accent=south_african: 南非
accent=southern_us: 美国南方|美国南部|德州
accent=french: 法国|法式
accent=german: 德国|德式
accent=spanish: 西班牙|墨西哥|拉丁
dialect=sichuan: 四川话|四川|川普|川渝|成都|重庆|四川口音|川味
dialect=northeastern: 东北话|东北|东北口音|东北腔|大碴子味|哈尔滨|沈阳
dialect=henan: 河南话|河南|河南口音|郑州
dialect=shaanxi: 陕西话|陕西|西安|陕西口音|关中
dialect=guizhou: 贵州话|贵州|贵阳
dialect=yunnan: 云南话|云南|昆明
dialect=guilin: 桂林话|桂林|广西
dialect=jinan: 济南话|济南|山东|山东话|山东口音
dialect=qingdao: 青岛话|青岛
dialect=shijiazhuang: 石家庄话|石家庄|河北|河北口音
dialect=gansu: 甘肃话|甘肃|兰州
dialect=ningxia: 宁夏话|宁夏|银川
dialect=beijing: 北京话|北京|京腔|京片子|老北京|北京口音|儿化音
dialect=tianjin: 天津话|天津|天津口音|津味
dialect=cantonese: 广普|广东普通话|广东口音|粤语口音|港普|香港|广东|粤式
dialect=taiwanese: 台湾腔|台湾|台普|台湾口音|湾湾|台湾国语
dialect=shanghai: 上海话|上海|沪普|上海口音|上海普通话
!neg: 不|不要|别|没有|没|无|毫无|并非|不是|不太|不能|切勿|勿
!int: 非常|特别|极其|超|超级|十分|无比|格外|极为
!stop: 很|相当|太|好|最|挺|的|地|得|了|着|一|个|位|名|种|是|有|带|带着|带有|略带|和|与|及|而|又|但|且|也|还|并|在|用|以|把|被|给|对|跟|让|请|像|似|比较|有点|有些|稍微|稍|略|一点|一些|点|些|声音|嗓音|音色|声线|嗓子|说话|讲话|讲|说|话|语气|口吻|口气|感觉|风格|听起来|听上去|般|样|一样|口音|腔|腔调|方言|普通话|国语|中文|汉语|英语|英文|语调|音调|语速|调|声|音|想要|要|需要|想|我|你|我们|适合|用来|来|做|当|成|就|都|会|能|可以|带点|一位|一个|一名|一种|人|者|为|作为|这|那|这个|那个|其|它|岁|左右|上下|多|年纪|年龄|气质|形象|角色|配音|朗读|读|念|讲述|讲故事|的话|带一点|好听|那种|这种|，|。|、
`
