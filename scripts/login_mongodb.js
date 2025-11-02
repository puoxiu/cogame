// 单机机模式初始化脚本（仅用于测试）
print('开始初始化单机MongoDB...');

// 切换到应用数据库
var gameDB = db.getSiblingDB('co_game');

// 创建应用用户（代码中使用该用户连接）
var appResult = gameDB.createUser({
    user: 'co_user',
    pwd: 'co_password123',
    roles: [{ role: 'readWrite', db: 'co_game' }]
});
print('应用用户创建结果:', JSON.stringify(appResult));

// 新增：创建测试用户（用于登录测试）
var testUser = {
    user_id: 10001,
    username: 'testuser',
    password: '8ccd4fe51cfc4dc51f51b43625998ee5', 
    email: 'test@example.com',
    created_at: new Date()
};
// 插入测试用户到users集合
var insertResult = gameDB.users.insertOne(testUser);
print('测试用户testuser创建结果:', JSON.stringify(insertResult));

// 创建登录功能必需的集合和索引
gameDB.users.createIndex({ "user_id": 1 }, { unique: true });
gameDB.users.createIndex({ "username": 1 }, { unique: true });
gameDB.users.createIndex({ "email": 1 });
print('用户集合合索引创建完成');

print('单机MongoDB初始化完成！');
