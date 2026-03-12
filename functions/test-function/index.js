// === 入力バリデーション（関数エントリーポイント） ===
exports.testFunction = async (req, res) => {
  try {
    const data = req.body;

    // 入力データのnullチェック
    if (!data || typeof data !== 'object') {
      console.error('Invalid input: request body is null or not an object', { body: req.body });
      return res.status(400).json({ error: 'Invalid request body' });
    }

    // 必須フィールドのバリデーション
    const requiredField = data.someField;
    if (requiredField == null) {
      console.error('Required field "someField" is null or undefined', { data });
      return res.status(400).json({ error: 'Missing required field: someField' });
    }

    // --- 42行目付近の修正例 ---
    // Before (NullPointerException の原因):
    //   const result = someObject.property.nestedMethod();
    //
    // After (null安全なアクセス + ガード句):
    const someObject = await fetchData(requiredField);
    if (!someObject || !someObject.property) {
      console.error('Unexpected null: someObject or someObject.property is null', {
        someObject,
        requiredField,
      });
      return res.status(500).json({ error: 'Internal error: unexpected null reference' });
    }
    const result = someObject.property.nestedMethod();

    return res.status(200).json({ result });
  } catch (error) {
    console.error('Unhandled exception in test-function', {
      message: error.message,
      stack: error.stack,
    });
    return res.status(500).json({ error: 'Internal server error' });
  }
};