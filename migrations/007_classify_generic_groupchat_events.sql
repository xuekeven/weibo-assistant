update weibo_messages
   set message_type = 'event',
       content = null
 where source = 'weibo_websocket'
   and message_type in ('text', 'system')
   and content ~ '^微博群聊事件（类型 [0-9]+）$';
